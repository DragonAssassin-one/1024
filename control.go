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
	if len(args) == 5 && (args[0] == "grant" || args[0] == "revoke") &&
		args[1] == githubConnection && args[3] == "--agent" && validAgent(args[4]) {
		repository, err := canonicalRepository(args[2])
		if err != nil {
			return err
		}
		store, err := storeForUser()
		if err != nil {
			return err
		}
		if args[0] == "grant" {
			enabled, err := store.enabled(args[4])
			if err != nil {
				return err
			}
			if !enabled {
				return errors.New("enable the GitHub connection before granting a repository")
			}
		}
		if err := store.setRepository(args[4], repository, args[0] == "grant"); err != nil {
			return err
		}
		fmt.Fprintf(output, "%s: GitHub repository %s %sed\n", args[4], repository, args[0])
		return nil
	}
	if len(args) != 4 || (args[0] != "connect" && args[0] != "disconnect") ||
		args[1] != githubConnection || args[2] != "--agent" || !validAgent(args[3]) {
		return errors.New("usage: 1024 credential github | connect|disconnect github --agent AGENT | grant|revoke github OWNER/REPO --agent AGENT")
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
