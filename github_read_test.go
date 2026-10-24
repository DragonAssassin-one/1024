package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
