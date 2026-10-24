package main

import (
	"errors"
	"fmt"
	"io"
	"os"
)

func control(args []string) error {
	if len(args) > 1 && args[1] == openAIConnection {
		return controlConnectionWithDeps(args, openAIConnection, defaultConnectionStore,
			openAICredentialExists, saveOpenAICredential, os.Stdout)
	}
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
	return controlConnectionWithDeps(args, githubConnection, storeForUser, credentialExists, saveCredential, output)
}

func controlConnectionWithDeps(args []string, service string, storeForUser func() (connectionStore, error),
	credentialExists func() (bool, error), saveCredential func() error, output io.Writer) error {
	if len(args) == 2 && args[0] == "credential" && args[1] == service {
		return saveCredential()
	}
	if len(args) != 4 || (args[0] != "connect" && args[0] != "disconnect") ||
		args[1] != service || args[2] != "--agent" || !validAgent(args[3]) {
		return errors.New("usage: 1024 credential SERVICE | connect|disconnect SERVICE --agent AGENT | grant|revoke github OWNER/REPO --agent AGENT")
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
			return fmt.Errorf("%s credential not found; use '1024 credential %s' first", service, service)
		}
	}
	if err := store.setService(args[3], service, enabled); err != nil {
		return err
	}
	state := "disabled"
	if enabled {
		state = "enabled"
	}
	label := "GitHub"
	if service == openAIConnection {
		label = "OpenAI"
	}
	fmt.Fprintf(output, "%s: %s %s\n", args[3], label, state)
	return nil
}
