package main

import (
	"context"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func runMCP(ctx context.Context) error {
	socket := os.Getenv(brokerSocketEnv)
	if _, err := readStatus(socket); err != nil {
		return err
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "1024", Version: "0.1.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "sandbox_status",
		Description: "Read the current sandbox's agent, authorized folder, and enabled connections.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input struct{}) (*mcp.CallToolResult, statusResponse, error) {
		status, err := readStatus(socket)
		return nil, status, err
	})
	return server.Run(ctx, &mcp.StdioTransport{})
}
