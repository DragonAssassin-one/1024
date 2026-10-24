package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitHubIdentityChecksAuthorizationEachCall(t *testing.T) {
	store := connectionStore{path: filepath.Join(t.TempDir(), "connections.json")}
	token := []byte("test-secret")
	reads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user" || r.Header.Get("Authorization") != "Bearer test-secret" {
			t.Errorf("unexpected request path or authorization")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"login":"user","id":42,"secret":"should-not-return"}`))
	}))
	defer server.Close()
	service := githubService{
		store: store,
		credential: func() ([]byte, error) {
			reads++
			return append([]byte(nil), token...), nil
		},
		client:   server.Client(),
		endpoint: server.URL + "/user",
	}
	if _, err := service.identity(context.Background(), "codex"); err == nil || reads != 0 {
		t.Fatal("disabled connection must not read keychain")
	}
	if err := store.set("codex", true); err != nil {
		t.Fatal(err)
	}
	identity, err := service.identity(context.Background(), "codex")
	if err != nil || identity.Login != "user" || identity.ID != 42 {
		t.Fatalf("identity: %+v, %v", identity, err)
	}
	if strings.Contains(identity.Login, "secret") || reads != 1 {
		t.Fatal("credential or unexpected response field was exposed")
	}
	if _, err := service.identity(context.Background(), "claude"); err == nil || reads != 1 {
		t.Fatal("authorization must be separate per agent")
	}
	if err := store.set("codex", false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.identity(context.Background(), "codex"); err == nil || reads != 1 {
		t.Fatal("revocation must take effect for the next call")
	}
}

func TestGitHubIdentityRejectsErrorBody(t *testing.T) {
	store := connectionStore{path: filepath.Join(t.TempDir(), "connections.json")}
	if err := store.set("dsh", true); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("test-secret"))
	}))
	defer server.Close()
	service := githubService{
		store: store, credential: func() ([]byte, error) { return []byte("test-secret"), nil },
		client: server.Client(), endpoint: server.URL,
	}
	_, err := service.identity(context.Background(), "dsh")
	if err == nil || strings.Contains(err.Error(), "test-secret") {
		t.Fatalf("response body leaked: %v", err)
	}
}

func TestGitHubIdentityRejectsRedirect(t *testing.T) {
	store := connectionStore{path: filepath.Join(t.TempDir(), "connections.json")}
	if err := store.set("codex", true); err != nil {
		t.Fatal(err)
	}
	redirected := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirected" {
			redirected = true
			t.Error("redirect target received credential")
			return
		}
		http.Redirect(w, r, "/redirected", http.StatusFound)
	}))
	defer server.Close()
	service := newGitHubService(store)
	service.endpoint = server.URL
	service.client.Transport = server.Client().Transport
	service.credential = func() ([]byte, error) { return []byte("test-secret"), nil }
	_, err := service.identity(context.Background(), "codex")
	if err == nil || redirected || strings.Contains(err.Error(), "test-secret") {
		t.Fatalf("redirect was not safely rejected: %v", err)
	}
}
