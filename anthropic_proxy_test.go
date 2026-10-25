package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAnthropicProxyAuthorizationAndHeaders(t *testing.T) {
	store := connectionStore{path: filepath.Join(t.TempDir(), "connections.json")}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "anthropic-secret" ||
			r.Header.Get("anthropic-version") != "2023-06-01" ||
			r.Header.Get("anthropic-beta") != "test-feature" {
			t.Errorf("missing Anthropic headers")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"msg_1","content":[{"type":"text","text":"ok"}]}`))
	}))
	defer upstream.Close()
	config := anthropicProxyConfig{
		store: store, upstream: upstream.URL, client: upstream.Client(),
		credential: func() ([]byte, error) { return []byte("anthropic-secret"), nil },
	}
	if _, err := startAnthropicProxy(config); err == nil {
		t.Fatal("proxy started while disabled")
	}
	if err := store.setService("claude", anthropicConnection, true); err != nil {
		t.Fatal(err)
	}
	proxy, err := startAnthropicProxy(config)
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	request, _ := http.NewRequest(http.MethodPost, proxy.baseURL()+"/v1/messages",
		strings.NewReader(`{"model":"test","messages":[]}`))
	request.Header.Set("X-Api-Key", proxy.capability)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Anthropic-Beta", "test-feature")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body := new(bytes.Buffer)
	body.ReadFrom(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || !bytes.Contains(body.Bytes(), []byte(`"text":"ok"`)) ||
		bytes.Contains(body.Bytes(), []byte("anthropic-secret")) {
		t.Fatalf("proxy response: %d %s", response.StatusCode, body)
	}
	if err := store.setService("claude", anthropicConnection, false); err != nil {
		t.Fatal(err)
	}
	request, _ = http.NewRequest(http.MethodPost, proxy.baseURL()+"/v1/messages",
		strings.NewReader(`{}`))
	request.Header.Set("X-Api-Key", proxy.capability)
	request.Header.Set("Content-Type", "application/json")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("revoked proxy accepted request: %d", response.StatusCode)
	}
}

func TestAnthropicProxyRejectsUnsafeRequests(t *testing.T) {
	store := connectionStore{path: filepath.Join(t.TempDir(), "connections.json")}
	store.setService("claude", anthropicConnection, true)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("event: message_stop\ndata: {}\n\n"))
	}))
	defer upstream.Close()
	proxy, err := startAnthropicProxy(anthropicProxyConfig{
		store: store, upstream: upstream.URL, client: upstream.Client(),
		credential: func() ([]byte, error) { return []byte("token"), nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	for _, request := range []struct {
		path  string
		body  string
		auth  string
		media string
		code  int
	}{
		{"/v1/models", "{}", proxy.capability, "application/json", http.StatusNotFound},
		{"/v1/messages", "{}", "wrong", "application/json", http.StatusForbidden},
		{"/v1/messages", "{}", "", "application/json", http.StatusForbidden},
		{"/v1/messages", "{", proxy.capability, "application/json", http.StatusBadRequest},
		{"/v1/messages", "{}", proxy.capability, "text/plain", http.StatusUnsupportedMediaType},
	} {
		req, _ := http.NewRequest(http.MethodPost, proxy.baseURL()+request.path,
			strings.NewReader(request.body))
		req.Header.Set("X-Api-Key", request.auth)
		req.Header.Set("Content-Type", request.media)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != request.code {
			t.Fatalf("%s got %d want %d", request.path, res.StatusCode, request.code)
		}
	}
}

func TestAnthropicProxyRejectsUnsafeUpstream(t *testing.T) {
	store := connectionStore{path: filepath.Join(t.TempDir(), "connections.json")}
	if err := store.setService("claude", anthropicConnection, true); err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		switch request.Model {
		case "upstream-error":
			http.Error(w, "upstream secret details", http.StatusUnauthorized)
		case "wrong-media":
			w.Header().Set("Content-Type", "text/plain")
			w.Write([]byte("upstream secret details"))
		case "leak":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"key":"upstream-key"}`))
		case "oversized":
			w.Header().Set("Content-Type", "application/json")
			w.Write(bytes.Repeat([]byte("a"), maxModelResponse+1))
		}
	}))
	defer upstream.Close()
	config := newAnthropicProxyConfig(store)
	if config.client.CheckRedirect == nil || config.upstream != "https://api.anthropic.com/v1/messages" {
		t.Fatal("production Anthropic target or redirect policy changed")
	}
	config.upstream, config.client = upstream.URL, upstream.Client()
	config.credential = func() ([]byte, error) { return []byte("upstream-key"), nil }
	proxy, err := startAnthropicProxy(config)
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	for _, model := range []string{"upstream-error", "wrong-media", "leak", "oversized"} {
		request, _ := http.NewRequest(http.MethodPost, proxy.baseURL()+"/v1/messages",
			strings.NewReader(`{"model":"`+model+`"}`))
		request.Header.Set("X-Api-Key", proxy.capability)
		request.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		body := new(bytes.Buffer)
		body.ReadFrom(response.Body)
		response.Body.Close()
		if response.StatusCode != http.StatusBadGateway ||
			bytes.Contains(body.Bytes(), []byte("upstream-key")) ||
			bytes.Contains(body.Bytes(), []byte("upstream secret details")) {
			t.Fatalf("%s: HTTP %d %s", model, response.StatusCode, body)
		}
	}
}

func TestAnthropicProxyMissingCredentialAndBrokenAuthorization(t *testing.T) {
	store := connectionStore{path: filepath.Join(t.TempDir(), "connections.json")}
	if err := store.setService("claude", anthropicConnection, true); err != nil {
		t.Fatal(err)
	}
	config := newAnthropicProxyConfig(store)
	config.credential = func() ([]byte, error) { return nil, errors.New("keychain unavailable") }
	proxy, err := startAnthropicProxy(config)
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	post := func() int {
		request, _ := http.NewRequest(http.MethodPost, proxy.baseURL()+"/v1/messages", strings.NewReader("{}"))
		request.Header.Set("X-Api-Key", proxy.capability)
		request.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		return response.StatusCode
	}
	if code := post(); code != http.StatusServiceUnavailable {
		t.Fatalf("missing credential: %d", code)
	}
	if err := os.WriteFile(store.path, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if code := post(); code != http.StatusForbidden {
		t.Fatalf("corrupt authorization: %d", code)
	}
	if _, err := startAnthropicProxy(config); err == nil {
		t.Fatal("proxy started with corrupt authorization")
	}
}

func TestAnthropicProxyDoesNotExposeTransportFailures(t *testing.T) {
	store := connectionStore{path: filepath.Join(t.TempDir(), "connections.json")}
	if err := store.setService("claude", anthropicConnection, true); err != nil {
		t.Fatal(err)
	}
	config := anthropicProxyConfig{
		store: store, upstream: "https://api.anthropic.com/v1/messages",
		credential: func() ([]byte, error) { return []byte("upstream-key"), nil },
		client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("private transport detail")
		})},
	}
	proxy, err := startAnthropicProxy(config)
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	request, _ := http.NewRequest(http.MethodPost, proxy.baseURL()+"/v1/messages", strings.NewReader("{}"))
	request.Header.Set("X-Api-Key", proxy.capability)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body := new(bytes.Buffer)
	body.ReadFrom(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusBadGateway ||
		bytes.Contains(body.Bytes(), []byte("upstream-key")) ||
		bytes.Contains(body.Bytes(), []byte("private transport detail")) {
		t.Fatalf("transport error escaped: %d %s", response.StatusCode, body)
	}

	config.upstream = ":"
	proxy2, err := startAnthropicProxy(config)
	if err != nil {
		t.Fatal(err)
	}
	defer proxy2.Close()
	request, _ = http.NewRequest(http.MethodPost, proxy2.baseURL()+"/v1/messages", strings.NewReader("{}"))
	request.Header.Set("X-Api-Key", proxy2.capability)
	request.Header.Set("Content-Type", "application/json")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusBadGateway {
		t.Fatalf("invalid upstream URL accepted: %d", response.StatusCode)
	}
}

func TestClaudeRunnerRejectsInvalidInputsBeforeDependencies(t *testing.T) {
	store := connectionStore{path: filepath.Join(t.TempDir(), "connections.json")}
	deps := func() (connectionStore, error) {
		t.Fatal("dependency lookup reached")
		return store, nil
	}
	for _, args := range [][]string{
		{},
		{"--folder", t.TempDir(), "--", "prompt"},
		{"--folder", t.TempDir(), "--model", "bad model", "--", "prompt"},
		{"--folder", t.TempDir(), "--model", "model", "--", "one", "two"},
	} {
		if err := runClaudeWithDeps(args, deps, func() (string, error) {
			t.Fatal("binary lookup reached")
			return "", nil
		}, newAnthropicProxyConfig, context.Background()); err == nil {
			t.Fatalf("invalid Claude input accepted: %v", args)
		}
	}
	if err := runClaude(nil); err == nil || !strings.Contains(err.Error(), "usage:") {
		t.Fatalf("invalid CLI invocation accepted: %v", err)
	}
}

func TestClaudeRunnerFailsClosedBeforeLaunch(t *testing.T) {
	store := connectionStore{path: filepath.Join(t.TempDir(), "connections.json")}
	args := []string{"--folder", t.TempDir(), "--model", "test-model", "--", "Reply briefly."}
	lookup := func() (connectionStore, error) { return store, nil }
	config := func(store connectionStore) anthropicProxyConfig {
		return anthropicProxyConfig{store: store}
	}
	if err := runClaudeWithDeps(args, lookup, func() (string, error) {
		return "", errors.New("Claude binary missing")
	}, config, context.Background()); err == nil || !strings.Contains(err.Error(), "binary missing") {
		t.Fatalf("missing CLI binary accepted: %v", err)
	}
	if err := runClaudeWithDeps(args, func() (connectionStore, error) {
		return connectionStore{}, errors.New("state unavailable")
	}, func() (string, error) { return "/usr/bin/true", nil },
		config, context.Background()); err == nil || !strings.Contains(err.Error(), "state unavailable") {
		t.Fatalf("missing authorization state accepted: %v", err)
	}
	overlap := []string{"--folder", args[1], "--read-only", args[1],
		"--model", "test-model", "--", "Reply briefly."}
	if err := runClaudeWithDeps(overlap, lookup, func() (string, error) {
		t.Fatal("binary lookup reached on overlapping folders")
		return "", nil
	}, config, context.Background()); err == nil || !strings.Contains(err.Error(), "overlaps") {
		t.Fatalf("overlapping folders accepted: %v", err)
	}
	missingReadOnly := []string{"--folder", args[1], "--read-only",
		filepath.Join(t.TempDir(), "missing"), "--model", "test-model", "--", "Reply briefly."}
	if err := runClaudeWithDeps(missingReadOnly, lookup, func() (string, error) {
		t.Fatal("binary lookup reached on missing reference")
		return "", nil
	}, config, context.Background()); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("missing read-only folder accepted: %v", err)
	}
	if err := runClaudeWithDeps(args, lookup, func() (string, error) {
		return "/usr/bin/true", nil
	}, config, context.Background()); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("disabled Anthropic connection launched CLI: %v", err)
	}
	if err := os.WriteFile(store.path, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := runClaudeWithDeps(args, lookup, func() (string, error) {
		return "/usr/bin/true", nil
	}, config, context.Background()); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("corrupt Anthropic connection launched CLI: %v", err)
	}
}

func TestClaudeRoutesThroughHostProxy(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS only")
	}
	if os.Getenv("KEYMOAT_SKIP_CLAUDE_SMOKE") == "1" {
		t.Skip("simulating CI without a Claude Code installation")
	}
	binary, err := findClaudeBinary()
	if err != nil {
		t.Skipf("Claude Code unavailable: %v", err)
	}
	root, err := os.MkdirTemp("/private/tmp", "1024-claude-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	store := connectionStore{path: filepath.Join(root, "state", "connections.json")}
	if err := store.setService("claude", anthropicConnection, true); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/v1/messages" || r.Header.Get("x-api-key") != "fake-anthropic-key" {
			t.Errorf("unexpected Anthropic request: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_test\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"test-model\",\"content\":[],\"stop_reason\":null,\"stop_sequence\":null,\"usage\":{\"input_tokens\":10,\"output_tokens\":0}}}\n\n" +
			"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"KeyMoat simulated answer\"}}\n\n" +
			"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
			"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":5}}\n\n" +
			"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
	}))
	defer upstream.Close()
	config := func(store connectionStore) anthropicProxyConfig {
		return anthropicProxyConfig{
			store: store, upstream: upstream.URL + "/v1/messages", client: upstream.Client(),
			credential: func() ([]byte, error) { return []byte("fake-anthropic-key"), nil },
		}
	}
	folder, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var output bytes.Buffer
	err = runClaudeTask([]string{"--folder", folder, "--model", "test-model", "--", "Reply briefly."},
		func() (connectionStore, error) { return store, nil },
		func() (string, error) { return binary, nil }, config, ctx, &output)
	if err != nil {
		t.Fatalf("Claude failed: %v; output: %s", err, output.String())
	}
	if calls.Load() == 0 || !strings.Contains(output.String(), "KeyMoat simulated answer") {
		t.Fatalf("Claude did not complete through host proxy (%d calls): %s", calls.Load(), output.String())
	}
	if strings.Contains(output.String(), "fake-anthropic-key") {
		t.Fatal("upstream key leaked to CLI output")
	}
	outside, err := os.CreateTemp("/private/tmp", "1024-claude-outside-")
	if err != nil {
		t.Fatal(err)
	}
	outside.Close()
	defer os.Remove(outside.Name())
	runtimeDir := filepath.Join("/private/tmp", "claude-"+strconv.Itoa(os.Getuid()))
	err = runPrepared("claude", []string{folder}, nil, "/bin/sh",
		[]string{"-c", `test -d "$1" && ! /bin/cat "$2" >/dev/null 2>&1`, "sh", runtimeDir, outside.Name()},
		runOptions{
			store: &store, extraRead: []string{runtimeDir}, extraReadMetadata: []string{"/tmp"},
		})
	if err != nil {
		t.Fatalf("Claude runtime exception exposed adjacent temp files: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "state", "sessions"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("session home remained: %v, %v", entries, err)
	}
}

func TestClaudeRunnerIsolatedLaunchWithoutInstalledCLI(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS only")
	}
	root, err := os.MkdirTemp("/private/tmp", "1024-claude-runner-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	store := connectionStore{path: filepath.Join(root, "state", "connections.json")}
	if err := store.setService("claude", anthropicConnection, true); err != nil {
		t.Fatal(err)
	}
	config := func(store connectionStore) anthropicProxyConfig {
		return anthropicProxyConfig{
			store: store, credential: func() ([]byte, error) {
				t.Error("mock CLI contacted Anthropic credential")
				return nil, nil
			},
		}
	}
	args := []string{"--folder", t.TempDir(), "--model", "test-model", "--", "Reply briefly."}
	if err := runClaudeWithDeps(args, func() (connectionStore, error) { return store, nil },
		func() (string, error) { return "/usr/bin/true", nil }, config, context.Background()); err != nil {
		t.Fatalf("isolated mock CLI failed: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "state", "sessions"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("temporary Claude home remained: %v, %v", entries, err)
	}
}

func TestClaudeProbeRejectsBroadReadGrant(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS only")
	}
	folder, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	err = runPrepared("claude", []string{folder}, nil, "/usr/bin/true", nil,
		runOptions{extraRead: []string{"/"}})
	if err == nil || !strings.Contains(err.Error(), "sandbox verification failed") {
		t.Fatalf("Claude launched after broad read permission: %v", err)
	}
}
