package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPRequiresBroker(t *testing.T) {
	t.Setenv(brokerSocketEnv, "")
	if err := runMCP(context.Background()); err == nil {
		t.Fatal("MCP should not start without the host broker")
	}
}

func TestMCPInsideSandbox(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS only")
	}
	binary := filepath.Join(t.TempDir(), "1024")
	build := exec.Command("go", "build", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v: %s", err, output)
	}
	project := t.TempDir()
	project, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatal(err)
	}
	readOnly, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "1024-test", Version: "0.1.0"}, nil)
	transport := &mcp.CommandTransport{Command: exec.Command(binary, "run", "--agent", "codex",
		"--folder", project, "--read-only", readOnly, "--", binary, "mcp")}
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tools, err := session.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 2 {
		t.Fatalf("tools: %+v, %v", tools, err)
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "sandbox_status"})
	if err != nil || result.IsError {
		t.Fatalf("call: %+v, %v", result, err)
	}
	status, ok := result.StructuredContent.(map[string]any)
	if !ok || status["agent"] != "codex" || status["folder"] != project {
		t.Fatalf("unexpected status: %#v", result.StructuredContent)
	}
	folders, ok := status["folders"].([]any)
	if !ok || len(folders) != 1 || folders[0] != project {
		t.Fatalf("unexpected authorized folders: %#v", status)
	}
	readOnlyFolders, ok := status["read_only_folders"].([]any)
	if !ok || len(readOnlyFolders) != 1 || readOnlyFolders[0] != readOnly {
		t.Fatalf("unexpected read-only folders: %#v", status)
	}
	if _, ok := status["connections"].([]any); !ok {
		t.Fatalf("expected connection list: %#v", status)
	}
}

func TestMCPToolAuthorizationChangesDuringSession(t *testing.T) {
	store := connectionStore{path: filepath.Join(t.TempDir(), "connections.json")}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"login":"coverage-user","id":42}`))
	}))
	defer api.Close()
	service := githubService{store: store, endpoint: api.URL, client: api.Client(),
		credential: func() ([]byte, error) { return []byte("test-token"), nil }}
	home, err := os.MkdirTemp("/private/tmp", "1024-mcp-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	broker, socket, err := startBroker(home, "codex", []string{"/project"}, nil, service)
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := newMCPServer(socket).Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0.0.1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	status, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "sandbox_status"})
	if err != nil || status.IsError {
		t.Fatalf("status: %+v, %v", status, err)
	}
	disabled, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "github_identity"})
	if err != nil || !disabled.IsError {
		t.Fatalf("disabled identity: %+v, %v", disabled, err)
	}
	if err := store.set("codex", true); err != nil {
		t.Fatal(err)
	}
	identity, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "github_identity"})
	if err != nil || identity.IsError {
		t.Fatalf("enabled identity: %+v, %v", identity, err)
	}
	data, ok := identity.StructuredContent.(map[string]any)
	if !ok || data["login"] != "coverage-user" {
		t.Fatalf("unexpected identity: %#v", identity.StructuredContent)
	}
	if err := store.set("codex", false); err != nil {
		t.Fatal(err)
	}
	revoked, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "github_identity"})
	if err != nil || !revoked.IsError {
		t.Fatalf("revoked identity: %+v, %v", revoked, err)
	}
}
