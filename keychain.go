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
)

func saveGitHubCredential() error {
	command := exec.Command("/usr/bin/security", "add-generic-password",
		"-U", "-a", githubKeychainAccount, "-s", githubKeychainService, "-w")
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
	return command.Run()
}

func githubCredentialExists() (bool, error) {
	command := exec.Command("/usr/bin/security", "find-generic-password",
		"-a", githubKeychainAccount, "-s", githubKeychainService)
	if err := command.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 44 {
			return false, nil
		}
		return false, fmt.Errorf("keychain lookup failed: %w", err)
	}
	return true, nil
}

func loadGitHubCredential() ([]byte, error) {
	command := exec.Command("/usr/bin/security", "find-generic-password",
		"-a", githubKeychainAccount, "-s", githubKeychainService, "-w")
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("keychain read failed: %w", err)
	}
	token := bytes.TrimSuffix(output, []byte{'\n'})
	if len(token) == 0 || len(token) > 4096 || bytes.ContainsAny(token, "\r\n") {
		return nil, errors.New("invalid GitHub credential in keychain")
	}
	return token, nil
}
