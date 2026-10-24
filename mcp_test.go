package main

import (
	"context"
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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "1024-test", Version: "0.1.0"}, nil)
	transport := &mcp.CommandTransport{Command: exec.Command(binary, "run", "--agent", "codex",
		"--folder", project, "--", binary, "mcp")}
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
	connections, ok := status["connections"].([]any)
	if !ok || len(connections) != 0 {
		t.Fatalf("expected empty connection list: %#v", status)
	}
	denied, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "github_identity"})
	if err != nil || !denied.IsError {
		t.Fatalf("disabled GitHub tool must fail: %+v, %v", denied, err)
	}
}
