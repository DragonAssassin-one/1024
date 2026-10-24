package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "__probe" {
		os.Exit(probe(os.Args[2:]))
	}

	var err error
	switch {
	case len(os.Args) > 1 && os.Args[1] == "status":
		err = printStatus()
	case len(os.Args) > 1 && os.Args[1] == "mcp":
		err = runMCP(context.Background())
	case len(os.Args) > 1 && os.Args[1] == "codex":
		err = runCodex(os.Args[2:])
	case len(os.Args) > 1 && os.Args[1] == "dashboard":
		err = runDashboard()
	case len(os.Args) > 1 && isAuthorizationCommand(os.Args[1]):
		err = control(os.Args[1:])
	default:
		err = runSandbox(os.Args[1:])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "1024:", err)
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() > 0 {
			os.Exit(exit.ExitCode())
		}
		os.Exit(1)
	}
}

func isAuthorizationCommand(command string) bool {
	switch command {
	case "credential", "connect", "disconnect", "grant", "revoke":
		return true
	default:
		return false
	}
}
