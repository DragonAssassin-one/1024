package main

import (
	"bytes"
	"errors"
	"os/exec"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestKeychainCommandsKeepCredentialOutOfArguments(t *testing.T) {
	var calls [][]string
	client := keychainClient{execute: func(args []string, interactive bool) ([]byte, error) {
		calls = append(calls, append([]string(nil), args...))
		if args[0] == "add-generic-password" && !interactive {
			t.Fatal("credential entry must be interactive")
		}
		if args[0] == "find-generic-password" && interactive {
			t.Fatal("lookup must not prompt for a new credential")
		}
		if args[0] == "find-generic-password" && args[len(args)-1] == "-w" {
			return []byte("test-token\n"), nil
		}
		return nil, nil
	}}
	if err := client.save(); err != nil {
		t.Fatal(err)
	}
	exists, err := client.exists()
	if err != nil || !exists {
		t.Fatalf("exists: %v, %v", exists, err)
	}
	token, err := client.load()
	if err != nil || string(token) != "test-token" {
		t.Fatalf("load failed: %v", err)
	}
	want := [][]string{
		{"add-generic-password", "-U", "-a", "github", "-s", "1024.github", "-w"},
		{"find-generic-password", "-a", "github", "-s", "1024.github"},
		{"find-generic-password", "-a", "github", "-s", "1024.github", "-w"},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("unexpected keychain arguments: %v", calls)
	}
}

func TestKeychainFailures(t *testing.T) {
	missing := exec.Command("/bin/sh", "-c", "exit 44").Run()
	failed := errors.New("security unavailable")
	for _, tc := range []struct {
		name    string
		failure error
		exists  bool
		wantErr bool
	}{
		{"missing", missing, false, false},
		{"other failure", failed, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := keychainClient{execute: func([]string, bool) ([]byte, error) {
				return nil, tc.failure
			}}
			exists, err := client.exists()
			if exists != tc.exists || (err != nil) != tc.wantErr {
				t.Fatalf("exists: %v, %v", exists, err)
			}
			if err := client.save(); err == nil {
				t.Fatal("save swallowed keychain error")
			}
			if token, err := client.load(); err == nil || token != nil ||
				strings.Contains(err.Error(), "test-token") {
				t.Fatalf("load exposed credential or swallowed error: %v", err)
			}
		})
	}
}

func TestKeychainRejectsInvalidCredential(t *testing.T) {
	for _, value := range [][]byte{nil, []byte("\n"), []byte("bad\nline"), []byte("bad\rline"),
		bytes.Repeat([]byte("x"), 4097)} {
		client := keychainClient{execute: func([]string, bool) ([]byte, error) {
			return value, nil
		}}
		if token, err := client.load(); err == nil || token != nil {
			t.Fatalf("accepted invalid credential of size %d", len(value))
		}
	}
}

func TestSecurityCommandFailsWithoutKeychainAccess(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS only")
	}
	// An invalid subcommand exercises process wiring without querying user items.
	if _, err := executeSecurity([]string{"1024-invalid-subcommand"}, false); err == nil {
		t.Fatal("invalid security command unexpectedly succeeded")
	}
	if _, err := executeSecurity([]string{"1024-invalid-subcommand"}, true); err == nil {
		t.Fatal("invalid interactive security command unexpectedly succeeded")
	}
}
