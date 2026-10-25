package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

func TestAnthropicConnectionScopedToClaude(t *testing.T) {
	store := connectionStore{path: filepath.Join(t.TempDir(), "connections.json")}
	var output bytes.Buffer
	run := func(args ...string) error {
		return controlConnectionWithDeps(args, anthropicConnection,
			func() (connectionStore, error) { return store, nil },
			func() (bool, error) { return true, nil },
			func() error { return nil }, &output)
	}
	if err := run("connect", "anthropic", "--agent", "claude"); err != nil {
		t.Fatal(err)
	}
	claude, _, err := store.status("claude")
	if err != nil || !slices.Contains(claude, anthropicConnection) {
		t.Fatalf("Claude connection not enabled: %v, %v", claude, err)
	}
	codex, _, err := store.status("codex")
	if err != nil || slices.Contains(codex, anthropicConnection) {
		t.Fatalf("Claude authorization leaked to Codex: %v, %v", codex, err)
	}
	if err := run("disconnect", "anthropic", "--agent", "claude"); err != nil ||
		!strings.Contains(output.String(), "Anthropic disabled") {
		t.Fatalf("Anthropic disconnect: %v, %s", err, output.String())
	}
}

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

func TestConnectionStoreRejectsInvalidFilesystemState(t *testing.T) {
	root := t.TempDir()
	parentFile := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(parentFile, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := (connectionStore{path: filepath.Join(parentFile, "connections.json")}).set("codex", true); err == nil {
		t.Fatal("accepted a non-directory state parent")
	}
	lockDir := filepath.Join(root, "locked", "connections.lock")
	if err := os.MkdirAll(lockDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := (connectionStore{path: filepath.Join(root, "locked", "connections.json")}).set("codex", true); err == nil {
		t.Fatal("accepted an unusable lock file")
	}
	path := filepath.Join(root, "null.json")
	if err := os.WriteFile(path, []byte("null"), 0600); err != nil {
		t.Fatal(err)
	}
	if enabled, err := (connectionStore{path: path}).enabled("codex"); err == nil || enabled {
		t.Fatalf("null state did not fail closed: %v, %v", enabled, err)
	}
	loop := filepath.Join(root, "loop.json")
	if err := os.Symlink("loop.json", loop); err != nil {
		t.Fatal(err)
	}
	if enabled, err := (connectionStore{path: loop}).enabled("codex"); err == nil || enabled {
		t.Fatalf("unreadable state did not fail closed: %v, %v", enabled, err)
	}
	if err := (connectionStore{path: filepath.Join(root, "valid.json")}).set("unknown", true); err == nil {
		t.Fatal("invalid agent was accepted")
	}
}

func TestControlRejectsUnavailableStateAndSaveFailure(t *testing.T) {
	failure := errors.New("host state unavailable")
	if err := controlWithDeps([]string{"connect", "github", "--agent", "codex"},
		func() (connectionStore, error) { return connectionStore{}, failure },
		func() (bool, error) { t.Fatal("keychain checked despite missing state"); return false, nil },
		func() error { return nil }, io.Discard); !errors.Is(err, failure) {
		t.Fatalf("expected host state failure, got %v", err)
	}
	if err := controlWithDeps([]string{"credential", "github"},
		func() (connectionStore, error) { return connectionStore{}, nil },
		func() (bool, error) { return false, nil },
		func() error { return failure }, io.Discard); !errors.Is(err, failure) {
		t.Fatalf("credential failure was swallowed: %v", err)
	}
	parent := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(parent, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := controlWithDeps([]string{"disconnect", "github", "--agent", "codex"},
		func() (connectionStore, error) {
			return connectionStore{path: filepath.Join(parent, "connections.json")}, nil
		},
		func() (bool, error) { t.Fatal("disconnect read keychain"); return false, nil },
		func() error { return nil }, io.Discard); err == nil {
		t.Fatal("authorization write failure was swallowed")
	}
}

func TestControlRepositoryGrantAndRevoke(t *testing.T) {
	store := connectionStore{path: filepath.Join(t.TempDir(), "connections.json")}
	authorize := func(args ...string) error {
		return controlWithDeps(args,
			func() (connectionStore, error) { return store, nil },
			func() (bool, error) { return true, nil },
			func() error { return nil }, io.Discard)
	}
	if err := authorize("grant", "github", "Owner/Repo", "--agent", "codex"); err == nil {
		t.Fatal("granted repository before enabling connection")
	}
	if err := authorize("connect", "github", "--agent", "codex"); err != nil {
		t.Fatal(err)
	}
	if err := authorize("grant", "github", "Owner/Repo", "--agent", "codex"); err != nil {
		t.Fatal(err)
	}
	allowed, err := store.repositoryAllowed("codex", "owner/repo")
	if err != nil || !allowed {
		t.Fatalf("repository grant: %v, %v", allowed, err)
	}
	if err := authorize("revoke", "github", "OWNER/REPO", "--agent", "codex"); err != nil {
		t.Fatal(err)
	}
	allowed, err = store.repositoryAllowed("codex", "owner/repo")
	if err != nil || allowed {
		t.Fatalf("repository revocation: %v, %v", allowed, err)
	}
}

func TestOpenAIConnectionIndependentAndFailClosed(t *testing.T) {
	store := connectionStore{path: filepath.Join(t.TempDir(), "connections.json")}
	var output bytes.Buffer
	exists := false
	controlOpenAI := func(args ...string) error {
		return controlConnectionWithDeps(args, openAIConnection,
			func() (connectionStore, error) { return store, nil },
			func() (bool, error) { return exists, nil },
			func() error { return nil }, &output)
	}
	if err := controlOpenAI("connect", "openai", "--agent", "codex"); err == nil {
		t.Fatal("enabled model connection without a credential")
	}
	exists = true
	if err := controlOpenAI("connect", "openai", "--agent", "codex"); err != nil {
		t.Fatal(err)
	}
	connections, repos, err := store.status("codex")
	if err != nil || !slices.Equal(connections, []string{"openai"}) || len(repos) != 0 {
		t.Fatalf("unexpected connections: %v %v %v", connections, repos, err)
	}
	enabled, err := store.enabledService("claude", openAIConnection)
	if err != nil || enabled {
		t.Fatalf("model grant leaked to another agent: %v, %v", enabled, err)
	}
	if err := controlOpenAI("disconnect", "openai", "--agent", "codex"); err != nil {
		t.Fatal(err)
	}
	enabled, err = store.enabledService("codex", openAIConnection)
	if err != nil || enabled || !strings.Contains(output.String(), "OpenAI disabled") {
		t.Fatalf("model grant remained active: %v, %v", enabled, err)
	}
	if err := store.setService("codex", "unknown", true); err == nil {
		t.Fatal("accepted arbitrary service")
	}
}

func TestRepositoryControlErrorsDoNotGrant(t *testing.T) {
	store := connectionStore{path: filepath.Join(t.TempDir(), "connections.json")}
	deps := func(args []string, getStore func() (connectionStore, error)) error {
		return controlWithDeps(args, getStore,
			func() (bool, error) { t.Fatal("unexpected credential lookup"); return false, nil },
			func() error { t.Fatal("unexpected credential save"); return nil }, io.Discard)
	}
	goodStore := func() (connectionStore, error) { return store, nil }
	if err := deps([]string{"grant", "github", "bad/path/extra", "--agent", "codex"}, goodStore); err == nil {
		t.Fatal("accepted malformed repository grant")
	}
	if err := deps([]string{"revoke", "github", "owner/repo", "--agent", "codex"},
		func() (connectionStore, error) { return connectionStore{}, errors.New("state unavailable") }); err == nil {
		t.Fatal("ignored unavailable host state")
	}
	if err := store.setService("codex", githubConnection, true); err != nil {
		t.Fatal(err)
	}
	if err := deps([]string{"grant", "github", "owner/repo", "--agent", "codex"}, goodStore); err != nil {
		t.Fatal(err)
	}
	connections, repositories, err := store.status("codex")
	if err != nil || !slices.Equal(connections, []string{"github"}) ||
		!slices.Equal(repositories, []string{"owner/repo"}) {
		t.Fatalf("grant state: %v %v %v", connections, repositories, err)
	}
	if err := store.setService("codex", githubConnection, false); err != nil {
		t.Fatal(err)
	}
	connections, repositories, err = store.status("codex")
	if err != nil || len(connections) != 0 || len(repositories) != 0 {
		t.Fatalf("disabled service advertised grant: %v %v %v", connections, repositories, err)
	}
	if err := os.WriteFile(store.path, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := deps([]string{"grant", "github", "owner/repo", "--agent", "codex"}, goodStore); err == nil {
		t.Fatal("corrupt state accepted grant")
	}
	if err := deps([]string{"revoke", "github", "owner/repo", "--agent", "codex"}, goodStore); err == nil {
		t.Fatal("corrupt state accepted revoke")
	}
}

func TestOpenAIControlDispatchRejectsInvalidAgent(t *testing.T) {
	if err := control([]string{"connect", "openai", "--agent", "invalid"}); err == nil {
		t.Fatal("invalid model connection agent accepted")
	}
}
