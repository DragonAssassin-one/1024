package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"sync"
	"testing"
)

func TestConnectionStorePerAgent(t *testing.T) {
	store := connectionStore{path: filepath.Join(t.TempDir(), "state", "connections.json")}
	for _, agent := range []string{"claude", "codex", "dsh"} {
		enabled, err := store.enabled(agent)
		if err != nil || enabled {
			t.Fatalf("default %s: %v, %v", agent, enabled, err)
		}
	}
	var group sync.WaitGroup
	for _, agent := range []string{"claude", "codex", "dsh"} {
		group.Add(1)
		go func() {
			defer group.Done()
			if err := store.set(agent, true); err != nil {
				t.Errorf("enable %s: %v", agent, err)
			}
		}()
	}
	group.Wait()
	for _, agent := range []string{"claude", "codex", "dsh"} {
		enabled, err := store.enabled(agent)
		if err != nil || !enabled {
			t.Fatalf("enable %s: %v, %v", agent, enabled, err)
		}
	}
	if err := store.set("codex", false); err != nil {
		t.Fatal(err)
	}
	enabled, err := store.enabled("codex")
	if err != nil || enabled {
		t.Fatalf("disable codex: %v, %v", enabled, err)
	}
	enabled, err = store.enabled("claude")
	if err != nil || !enabled {
		t.Fatalf("claude should remain enabled: %v, %v", enabled, err)
	}
}

func TestConnectionStoreCorruptionFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "connections.json")
	if err := os.WriteFile(path, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	store := connectionStore{path: path}
	if enabled, err := store.enabled("codex"); err == nil || enabled {
		t.Fatalf("corrupt state must fail closed: %v, %v", enabled, err)
	}
	if err := store.set("codex", true); err == nil {
		t.Fatal("corrupt state should not be overwritten")
	}
}

func TestProtectedFolders(t *testing.T) {
	current, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	home, err := filepath.EvalSymlinks(current.HomeDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateProject(home); err == nil {
		t.Fatal("home directory would expose host connection state")
	}
	if err := validateProject(t.TempDir()); err != nil {
		t.Fatalf("unrelated project should be allowed: %v", err)
	}
	if !containsPath("/a/b", "/a/b/c") || containsPath("/a/b", "/a/bad") {
		t.Fatal("directory relationship is incorrect")
	}
}

func TestControlRejectsInvalidInput(t *testing.T) {
	for _, args := range [][]string{
		{"connect", "github", "--agent", "other"},
		{"connect", "other", "--agent", "codex"},
		{"credential", "github", "secret-as-argument"},
	} {
		if err := control(args); err == nil {
			t.Fatalf("accepted invalid control command: %v", args)
		}
	}
}

func TestControlConnectionLifecycle(t *testing.T) {
	store := connectionStore{path: filepath.Join(t.TempDir(), "state", "connections.json")}
	var output bytes.Buffer
	saved := false
	exists := false
	deps := func(args ...string) error {
		return controlWithDeps(args,
			func() (connectionStore, error) { return store, nil },
			func() (bool, error) { return exists, nil },
			func() error { saved = true; return nil }, &output)
	}
	if err := deps("credential", "github"); err != nil || !saved {
		t.Fatalf("credential: %v, saved=%v", err, saved)
	}
	if err := deps("connect", "github", "--agent", "codex"); err == nil {
		t.Fatal("connected without a credential")
	}
	exists = true
	if err := deps("connect", "github", "--agent", "codex"); err != nil {
		t.Fatal(err)
	}
	enabled, err := store.enabled("codex")
	if err != nil || !enabled {
		t.Fatalf("connection was not enabled: %v, %v", enabled, err)
	}
	if err := deps("disconnect", "github", "--agent", "codex"); err != nil {
		t.Fatal(err)
	}
	enabled, err = store.enabled("codex")
	if err != nil || enabled || !bytes.Contains(output.Bytes(), []byte("GitHub disabled")) {
		t.Fatalf("connection was not disabled: %v, %v, %s", enabled, err, output.String())
	}
}

func TestControlFailsClosedOnCredentialLookup(t *testing.T) {
	store := connectionStore{path: filepath.Join(t.TempDir(), "connections.json")}
	err := controlWithDeps([]string{"connect", "github", "--agent", "claude"},
		func() (connectionStore, error) { return store, nil },
		func() (bool, error) { return false, errors.New("keychain unavailable") },
		func() error { t.Fatal("credential save was called"); return nil }, io.Discard)
	if err == nil {
		t.Fatal("connected after keychain lookup failure")
	}
	enabled, err := store.enabled("claude")
	if err != nil || enabled {
		t.Fatalf("authorization was changed: %v, %v", enabled, err)
	}
}
