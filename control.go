package main

import (
	"errors"
	"fmt"
	"io"
	"os"
)

func control(args []string) error {
	return controlWithDeps(args, defaultConnectionStore, githubCredentialExists, saveGitHubCredential, os.Stdout)
}

func controlWithDeps(args []string, storeForUser func() (connectionStore, error),
	credentialExists func() (bool, error), saveCredential func() error, output io.Writer) error {
	if len(args) == 2 && args[0] == "credential" && args[1] == githubConnection {
		return saveCredential()
	}
	if len(args) != 4 || (args[0] != "connect" && args[0] != "disconnect") ||
		args[1] != githubConnection || args[2] != "--agent" || !validAgent(args[3]) {
		return errors.New("usage: 1024 credential github | connect|disconnect github --agent claude|codex|dsh")
	}
	store, err := storeForUser()
	if err != nil {
		return err
	}
	enabled := args[0] == "connect"
	if enabled {
		exists, err := credentialExists()
		if err != nil {
			return err
		}
		if !exists {
			return errors.New("GitHub credential not found; use '1024 credential github' first")
		}
	}
	if err := store.set(args[3], enabled); err != nil {
		return err
	}
	state := "disabled"
	if enabled {
		state = "enabled"
	}
	fmt.Fprintf(output, "%s: GitHub %s\n", args[3], state)
	return nil
}
