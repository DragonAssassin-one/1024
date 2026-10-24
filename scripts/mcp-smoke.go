//go:build ignore

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: mcp-smoke BINARY PROJECT")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "coverage-smoke", Version: "0.0.1"}, nil)
	command := exec.Command(os.Args[1], "run", "--agent", "codex", "--folder", os.Args[2],
		"--", os.Args[1], "mcp")
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: command}, nil)
	if err != nil {
		fail(err)
	}
	defer session.Close()
	tools, err := session.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 2 {
		fail(fmt.Errorf("expected two MCP tools, got %v: %w", tools, err))
	}
	status, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "sandbox_status"})
	if err != nil || status.IsError {
		fail(fmt.Errorf("sandbox_status: %v, %w", status, err))
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
