package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepositoryGrantIsPerAgentAndRevocable(t *testing.T) {
	store := connectionStore{path: filepath.Join(t.TempDir(), "connections.json")}
	if err := store.setRepository("codex", "Owner/Repo", true); err != nil {
		t.Fatal(err)
	}
	allowed, err := store.repositoryAllowed("codex", "owner/repo")
	if err != nil || allowed {
		t.Fatalf("repository grant bypassed connection toggle: %v, %v", allowed, err)
	}
	if err := store.set("codex", true); err != nil {
		t.Fatal(err)
	}
	allowed, err = store.repositoryAllowed("codex", "owner/repo")
	if err != nil || !allowed {
		t.Fatalf("repository grant missing: %v, %v", allowed, err)
	}
	allowed, err = store.repositoryAllowed("claude", "owner/repo")
	if err != nil || allowed {
		t.Fatalf("repository grant leaked to another agent: %v, %v", allowed, err)
	}
	if err := store.setRepository("codex", "OWNER/REPO", false); err != nil {
		t.Fatal(err)
	}
	allowed, err = store.repositoryAllowed("codex", "owner/repo")
	if err != nil || allowed {
		t.Fatalf("repository grant was not revoked: %v, %v", allowed, err)
	}
	for _, bad := range []string{"", "/repo", "owner/", "owner/../repo", "owner/%2e%2e", "owner//repo"} {
		if err := store.setRepository("codex", bad, true); err == nil {
			t.Fatalf("accepted invalid repository %q", bad)
		}
	}
}

func TestGitHubReadFileScopeAndResponse(t *testing.T) {
	store := connectionStore{path: filepath.Join(t.TempDir(), "connections.json")}
	reads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.EscapedPath() != "/repos/owner/repo/contents/docs/hello%20world.md" ||
			r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("unexpected upstream request: %s %s", r.Method, r.URL.EscapedPath())
		}
		json.NewEncoder(w).Encode(map[string]any{
			"type": "file", "encoding": "base64", "size": 5,
			"content": base64.StdEncoding.EncodeToString([]byte("hello")),
			"extra":   "must-not-return",
		})
	}))
	defer server.Close()
	service := githubService{store: store, client: server.Client(), apiBase: server.URL,
		credential: func() ([]byte, error) { reads++; return []byte("test-token"), nil }}
	if _, err := service.readFile(context.Background(), "codex", "owner/repo", "docs/hello world.md"); err == nil || reads != 0 {
		t.Fatalf("read credential before connection: %v", err)
	}
	if err := store.set("codex", true); err != nil {
		t.Fatal(err)
	}
	if _, err := service.readFile(context.Background(), "codex", "owner/repo", "docs/hello world.md"); err == nil || reads != 0 {
		t.Fatalf("read credential before repo grant: %v", err)
	}
	if err := store.setRepository("codex", "Owner/Repo", true); err != nil {
		t.Fatal(err)
	}
	file, err := service.readFile(context.Background(), "codex", "OWNER/REPO", "docs/hello world.md")
	if err != nil || file.Repository != "owner/repo" || file.Content != "hello" || reads != 1 ||
		strings.Contains(file.Content, "must-not-return") {
		t.Fatalf("filtered content: %+v, %v", file, err)
	}
	for _, bad := range []string{"../secret", "/etc/passwd", "docs//file", "docs\\file", "docs/./file"} {
		if _, err := service.readFile(context.Background(), "codex", "owner/repo", bad); err == nil || reads != 1 {
			t.Fatalf("accepted path %q before credential check: %v", bad, err)
		}
	}
	if err := store.setRepository("codex", "owner/repo", false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.readFile(context.Background(), "codex", "owner/repo", "docs/hello world.md"); err == nil || reads != 1 {
		t.Fatalf("revoked repository reached credential: %v", err)
	}
}

func TestGitHubReadFileRejectsUnsafeContent(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"directory", 200, `{"type":"dir","encoding":"base64","size":0,"content":""}`},
		{"binary", 200, `{"type":"file","encoding":"base64","size":2,"content":"/wA="}`},
		{"credential reflection", 200, `{"type":"file","encoding":"base64","size":10,"content":"dGVzdC10b2tlbg=="}`},
		{"size", 200, `{"type":"file","encoding":"base64","size":65537,"content":""}`},
		{"bad encoding", 200, `{"type":"file","encoding":"base64","size":5,"content":"!"}`},
		{"mismatched size", 200, `{"type":"file","encoding":"base64","size":4,"content":"aGVsbG8="}`},
		{"oversized body", 200, strings.Repeat("x", 151<<10)},
		{"upstream failure", 403, "test-token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := connectionStore{path: filepath.Join(t.TempDir(), "connections.json")}
			store.set("codex", true)
			store.setRepository("codex", "owner/repo", true)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.body))
			}))
			defer server.Close()
			service := githubService{store: store, client: server.Client(), apiBase: server.URL,
				credential: func() ([]byte, error) { return []byte("test-token"), nil }}
			_, err := service.readFile(context.Background(), "codex", "owner/repo", "README.md")
			if err == nil || strings.Contains(err.Error(), "test-token") {
				t.Fatalf("unsafe response was accepted or echoed: %v", err)
			}
		})
	}
}

func TestGitHubReadFileChecksStateAndCredentialFirst(t *testing.T) {
	store := connectionStore{path: filepath.Join(t.TempDir(), "connections.json")}
	if err := store.setService("codex", githubConnection, true); err != nil {
		t.Fatal(err)
	}
	if err := store.setRepository("codex", "owner/repo", true); err != nil {
		t.Fatal(err)
	}
	reads := 0
	service := githubService{store: store, credential: func() ([]byte, error) {
		reads++
		return nil, errors.New("private keychain error")
	}}
	if _, err := service.readFile(context.Background(), "codex", "owner/repo", "README.md"); err == nil ||
		strings.Contains(err.Error(), "private") || reads != 1 {
		t.Fatalf("credential failure leaked: %v", err)
	}
	if err := os.WriteFile(store.path, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.readFile(context.Background(), "codex", "owner/repo", "README.md"); err == nil ||
		reads != 1 {
		t.Fatalf("corrupt grant read credential: %v", err)
	}
}

func TestGitHubReadFileRejectsRedirectAndNetworkFailure(t *testing.T) {
	store := connectionStore{path: filepath.Join(t.TempDir(), "connections.json")}
	store.setService("codex", githubConnection, true)
	store.setRepository("codex", "owner/repo", true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/other" {
			t.Error("redirect target received credential")
		}
		http.Redirect(w, r, "/other", http.StatusFound)
	}))
	service := newGitHubService(store)
	service.credential = func() ([]byte, error) { return []byte("test-token"), nil }
	service.apiBase = server.URL
	service.client.Transport = server.Client().Transport
	if _, err := service.readFile(context.Background(), "codex", "owner/repo", "README.md"); err == nil {
		t.Fatal("redirect was followed")
	}
	server.Close()
	if _, err := service.readFile(context.Background(), "codex", "owner/repo", "README.md"); err == nil {
		t.Fatal("unreachable upstream was accepted")
	}
}

func TestGitHubReadFileRejectsMalformedUpstreamConfiguration(t *testing.T) {
	store := connectionStore{path: filepath.Join(t.TempDir(), "connections.json")}
	store.setService("codex", githubConnection, true)
	store.setRepository("codex", "owner/repo", true)
	service := githubService{
		store: store, apiBase: ":invalid-url", client: http.DefaultClient,
		credential: func() ([]byte, error) { return []byte("test-token"), nil },
	}
	if _, err := service.readFile(context.Background(), "codex", "owner/repo", "README.md"); err == nil {
		t.Fatal("invalid upstream URL was accepted")
	}
	if _, err := repositoryFilePath(strings.Repeat("x", 241)); err == nil {
		t.Fatal("oversized repository path was accepted")
	}
}

func TestGitHubReadFileRejectsUnauthorizedInputsBeforeCredential(t *testing.T) {
	store := connectionStore{path: filepath.Join(t.TempDir(), "connections.json")}
	store.setService("codex", githubConnection, true)
	service := githubService{store: store, credential: func() ([]byte, error) {
		t.Fatal("unauthorized input reached keychain")
		return nil, nil
	}}
	for _, tc := range []struct{ repo, name string }{
		{"owner/repo/extra", "README.md"},
		{"owner/repo", ""},
		{"owner/repo", "../secret"},
		{"owner/repo", "README.md"},
	} {
		if _, err := service.readFile(context.Background(), "codex", tc.repo, tc.name); err == nil {
			t.Fatalf("invalid or ungranted request was accepted: %+v", tc)
		}
	}
}

func TestGitHubReadFileUsesFixedDefaultUpstream(t *testing.T) {
	store := connectionStore{path: filepath.Join(t.TempDir(), "connections.json")}
	store.setService("codex", githubConnection, true)
	store.setRepository("codex", "owner/repo", true)
	service := githubService{
		store: store,
		client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.String() != "https://api.github.com/repos/owner/repo/contents/README.md" {
				t.Errorf("unexpected default URL: %s", r.URL)
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(
				`{"type":"file","encoding":"base64","size":2,"content":"b2s="}`)),
				Header: make(http.Header)}, nil
		})},
		credential: func() ([]byte, error) { return []byte("test-token"), nil },
	}
	file, err := service.readFile(context.Background(), "codex", "owner/repo", "README.md")
	if err != nil || file.Content != "ok" {
		t.Fatalf("default upstream failed: %+v, %v", file, err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}
