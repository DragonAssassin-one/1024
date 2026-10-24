package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCodexConfigDoesNotContainUpstreamKey(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	proxy := &modelProxy{capability: "local-capability", listener: listener}
	config := codexConfig("test-model", proxy, "/path/to/1024")
	for _, expected := range []string{"test-model", proxy.baseURL(), "local-capability",
		`command = "/path/to/1024"`} {
		if !strings.Contains(config, expected) {
			t.Fatalf("missing %q from config", expected)
		}
	}
	if strings.Contains(config, "upstream-secret") {
		t.Fatal("upstream key appeared in config")
	}
}

func TestCodexIsolatedNativeLaunch(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS only")
	}
	binary, err := findCodexBinary()
	if err != nil {
		t.Skipf("Codex native binary unavailable: %v", err)
	}
	store := connectionStore{path: filepath.Join(t.TempDir(), "connections.json")}
	if err := store.setService("codex", openAIConnection, true); err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("version check unexpectedly contacted model service")
	}))
	defer upstream.Close()
	proxy, err := startModelProxy(modelProxyConfig{
		store: store, upstream: upstream.URL, client: upstream.Client(),
		credential: func() ([]byte, error) { t.Error("read model credential"); return nil, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	folder, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	err = runPrepared("codex", []string{folder}, nil, binary, []string{"--version"}, runOptions{
		proxyPort: proxy.port(),
		prepare: func(home, socket, self string) ([]string, error) {
			codexHome := filepath.Join(home, ".codex")
			if err := os.Mkdir(codexHome, 0700); err != nil {
				return nil, err
			}
			if err := os.WriteFile(filepath.Join(codexHome, "config.toml"),
				[]byte(codexConfig("test-model", proxy, self)), 0600); err != nil {
				return nil, err
			}
			return []string{"CODEX_HOME=" + codexHome}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCodexRoutesModelRequestToHost(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS only")
	}
	binary, err := findCodexBinary()
	if err != nil {
		t.Skipf("Codex native binary unavailable: %v", err)
	}
	store := connectionStore{path: filepath.Join(t.TempDir(), "connections.json")}
	if err := store.setService("codex", openAIConnection, true); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/v1/responses" || r.Header.Get("Authorization") != "Bearer fake-model-key" {
			t.Errorf("unexpected model request: %s, auth present=%v", r.URL.Path, r.Header.Get("Authorization") != "")
		}
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":{"message":"fake model"}}`))
	}))
	defer upstream.Close()
	proxy, err := startModelProxy(modelProxyConfig{
		store: store, upstream: upstream.URL + "/v1/responses", client: upstream.Client(),
		credential: func() ([]byte, error) { return []byte("fake-model-key"), nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	folder, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	err = runPrepared("codex", []string{folder}, nil, binary,
		[]string{"exec", "--ephemeral", "--skip-git-repo-check", "--sandbox", "danger-full-access", "--cd", folder,
			"Say hello."}, runOptions{
			ctx: ctx, proxyPort: proxy.port(), nonInteractive: true,
			extraRead: []string{"/etc/codex/requirements.toml", "/private/etc/codex/requirements.toml"},
			prepare: func(home, socket, self string) ([]string, error) {
				codexHome := filepath.Join(home, ".codex")
				if err := os.Mkdir(codexHome, 0700); err != nil {
					return nil, err
				}
				if err := os.WriteFile(filepath.Join(codexHome, "config.toml"),
					[]byte(codexConfig("gpt-5", proxy, self)), 0600); err != nil {
					return nil, err
				}
				return []string{"CODEX_HOME=" + codexHome}, nil
			},
		})
	if err == nil {
		t.Fatal("fake upstream unexpectedly completed Codex task")
	}
	if calls.Load() == 0 {
		t.Fatalf("Codex did not reach host model proxy: %v", err)
	}
}

func TestCodexCompletesWithSimulatedModel(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS only")
	}
	binary, err := findCodexBinary()
	if err != nil {
		t.Skipf("Codex native binary unavailable: %v", err)
	}
	store := connectionStore{path: filepath.Join(t.TempDir(), "connections.json")}
	store.setService("codex", openAIConnection, true)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" || r.Header.Get("Authorization") != "Bearer fake-model-key" {
			t.Errorf("unexpected model request: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(`event: response.completed
data: {"type":"response.completed","response":{"id":"resp_demo","object":"response","created_at":1760000000,"model":"gpt-5","status":"completed","output":[{"id":"msg_demo","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"KeyMoat simulated answer","annotations":[]}]}],"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}}

`))
	}))
	defer upstream.Close()
	proxy, err := startModelProxy(modelProxyConfig{
		store: store, upstream: upstream.URL + "/v1/responses", client: upstream.Client(),
		credential: func() ([]byte, error) { return []byte("fake-model-key"), nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	folder, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	err = runPrepared("codex", []string{folder}, nil, binary,
		[]string{"exec", "--ephemeral", "--skip-git-repo-check", "--sandbox", "danger-full-access",
			"--cd", folder, "Respond with one short sentence."}, runOptions{
			ctx: ctx, proxyPort: proxy.port(), nonInteractive: true,
			extraRead: []string{"/etc/codex/requirements.toml", "/private/etc/codex/requirements.toml"},
			prepare: func(home, socket, self string) ([]string, error) {
				codexHome := filepath.Join(home, ".codex")
				if err := os.Mkdir(codexHome, 0700); err != nil {
					return nil, err
				}
				if err := os.WriteFile(filepath.Join(codexHome, "config.toml"),
					[]byte(codexConfig("gpt-5", proxy, self)), 0600); err != nil {
					return nil, err
				}
				return []string{"CODEX_HOME=" + codexHome}, nil
			},
		})
	if err != nil {
		t.Fatalf("simulated Codex response failed: %v", err)
	}
}

func TestCodexModeUsesIsolatedHomeAndModelProxy(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS only")
	}
	root, err := os.MkdirTemp("/private/tmp", "1024-state-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	store := connectionStore{path: filepath.Join(root, "state", "connections.json")}
	if err := store.setService("codex", openAIConnection, true); err != nil {
		t.Fatal(err)
	}
	folder := t.TempDir()
	lookup := func() (connectionStore, error) { return store, nil }
	binary := func() (string, error) { return "/usr/bin/false", nil }
	config := func(store connectionStore) modelProxyConfig {
		return modelProxyConfig{store: store, credential: func() ([]byte, error) {
			t.Error("false command should not reach model credential")
			return nil, nil
		}}
	}
	err = runCodexWithDeps([]string{"--folder", folder, "--model", "gpt-5", "--", "Say hello"},
		lookup, binary, config, context.Background())
	if err == nil {
		t.Fatal("false command unexpectedly succeeded")
	}
	entries, err := os.ReadDir(filepath.Join(root, "state", "sessions"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("session home remained after command: %v, %v", entries, err)
	}
	err = runCodexWithDeps([]string{"--folder", folder, "--model", "gpt-5", "--", "Say hello"},
		lookup, func() (string, error) { return "/usr/bin/true", nil }, config, context.Background())
	if err != nil {
		t.Fatalf("successful sandboxed command failed: %v", err)
	}
	entries, err = os.ReadDir(filepath.Join(root, "state", "sessions"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("session home remained after success: %v, %v", entries, err)
	}
	if err := store.setService("codex", openAIConnection, false); err != nil {
		t.Fatal(err)
	}
	err = runCodexWithDeps([]string{"--folder", folder, "--model", "gpt-5", "--", "Say hello"},
		lookup, binary, config, context.Background())
	if err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("disabled connection launched command: %v", err)
	}
}

func TestCodexModeRejectsInvalidInputs(t *testing.T) {
	root := t.TempDir()
	lookup := func() (connectionStore, error) {
		t.Fatal("looked up authorization for invalid input")
		return connectionStore{}, nil
	}
	binary := func() (string, error) {
		t.Fatal("looked up Codex for invalid input")
		return "", nil
	}
	for _, args := range [][]string{
		{"--unknown"},
		{"--folder", root, "--", "prompt"},
		{"--folder", root, "--model", "bad\nmodel", "--", "prompt"},
		{"--model", "gpt-5", "--", "prompt"},
		{"--folder", root, "--read-only", root, "--model", "gpt-5", "--", "prompt"},
		{"--folder", root, "--model", "gpt-5", "--", "one", "two"},
		{"--folder", filepath.Join(root, "missing"), "--model", "gpt-5", "--", "prompt"},
		{"--folder", root, "--read-only", filepath.Join(root, "missing"), "--model", "gpt-5", "--", "prompt"},
	} {
		if err := runCodexWithDeps(args, lookup, binary, newModelProxyConfig, context.Background()); err == nil {
			t.Fatalf("accepted invalid Codex command: %v", args)
		}
	}
	if err := runCodex([]string{"--model", "gpt-5", "--", "prompt"}); err == nil {
		t.Fatal("production entrypoint accepted missing folder")
	}
	for _, tc := range []struct {
		name   string
		store  func() (connectionStore, error)
		native func() (string, error)
	}{
		{"binary unavailable", lookup, func() (string, error) { return "", errors.New("missing binary") }},
		{"host state unavailable", func() (connectionStore, error) {
			return connectionStore{}, errors.New("missing state")
		}, func() (string, error) { return "/usr/bin/false", nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := runCodexWithDeps([]string{"--folder", root, "--model", "gpt-5", "--", "prompt"},
				tc.store, tc.native, newModelProxyConfig, context.Background()); err == nil {
				t.Fatal("unavailable dependency was accepted")
			}
		})
	}
	if err := runCodexWithDeps([]string{"--folder", root, "--model", "gpt-5", "--", "prompt"},
		func() (connectionStore, error) { return connectionStore{}, errors.New("state unavailable") },
		func() (string, error) { return "/usr/bin/true", nil }, newModelProxyConfig, context.Background()); err == nil {
		t.Fatal("unavailable state accepted")
	}
	stateFile := filepath.Join(root, "file")
	if err := os.WriteFile(stateFile, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := runCodexWithDeps([]string{"--folder", root, "--model", "gpt-5", "--", "prompt"},
		func() (connectionStore, error) {
			return connectionStore{path: filepath.Join(stateFile, "connections.json")}, nil
		}, func() (string, error) { return "/usr/bin/true", nil },
		newModelProxyConfig, context.Background()); err == nil {
		t.Fatal("invalid authorization path accepted")
	}
}
