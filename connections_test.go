package main

import (
	"os"
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
