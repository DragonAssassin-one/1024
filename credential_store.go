package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
)

const (
	githubKeychainService = "1024.github"
	githubKeychainAccount = "github"
	openAIKeychainService = "1024.openai"
	openAIKeychainAccount = "openai"
)

func saveGitHubCredential() error {
	return (keychainClient{execute: executeSecurity}).save()
}

func githubCredentialExists() (bool, error) {
	return (keychainClient{execute: executeSecurity}).exists()
}

func loadGitHubCredential() ([]byte, error) {
	return (keychainClient{execute: executeSecurity}).load()
}

func saveOpenAICredential() error {
	return (keychainClient{execute: executeSecurity}).saveFor(openAIKeychainAccount, openAIKeychainService)
}

func openAICredentialExists() (bool, error) {
	return (keychainClient{execute: executeSecurity}).existsFor(openAIKeychainAccount, openAIKeychainService)
}

func loadOpenAICredential() ([]byte, error) {
	return (keychainClient{execute: executeSecurity}).loadFor(openAIKeychainAccount, openAIKeychainService)
}

type keychainClient struct {
	execute func(args []string, interactive bool) ([]byte, error)
}

func executeSecurity(args []string, interactive bool) ([]byte, error) {
	command := exec.Command("/usr/bin/security", args...)
	if interactive {
		command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
		return nil, command.Run()
	}
	return command.Output()
}

func (c keychainClient) save() error {
	return c.saveFor(githubKeychainAccount, githubKeychainService)
}

func (c keychainClient) saveFor(account, service string) error {
	_, err := c.execute([]string{"add-generic-password", "-U", "-a", account,
		"-s", service, "-w"}, true)
	return err
}

func (c keychainClient) exists() (bool, error) {
	return c.existsFor(githubKeychainAccount, githubKeychainService)
}

func (c keychainClient) existsFor(account, service string) (bool, error) {
	_, err := c.execute([]string{"find-generic-password", "-a", account,
		"-s", service}, false)
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 44 {
			return false, nil
		}
		return false, fmt.Errorf("keychain lookup failed: %w", err)
	}
	return true, nil
}

func (c keychainClient) load() ([]byte, error) {
	return c.loadFor(githubKeychainAccount, githubKeychainService)
}

func (c keychainClient) loadFor(account, service string) ([]byte, error) {
	output, err := c.execute([]string{"find-generic-password", "-a", account,
		"-s", service, "-w"}, false)
	if err != nil {
		return nil, fmt.Errorf("keychain read failed: %w", err)
	}
	token := bytes.TrimSuffix(output, []byte{'\n'})
	if len(token) == 0 || len(token) > 4096 || bytes.ContainsAny(token, "\r\n") {
		return nil, errors.New("invalid GitHub credential in keychain")
	}
	return token, nil
}
