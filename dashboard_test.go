package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func dashboardServer(t *testing.T) (*dashboard, *httptest.Server) {
	t.Helper()
	store := connectionStore{path: filepath.Join(t.TempDir(), "connections.json")}
	d, err := newDashboard(store, "")
	if err != nil {
		t.Fatal(err)
	}
	d.openAICredential = func() (bool, error) { return true, nil }
	d.gitHubCredential = func() (bool, error) { return true, nil }
	server := httptest.NewServer(d.handler())
	d.host = strings.TrimPrefix(server.URL, "http://")
	t.Cleanup(server.Close)
	return d, server
}

func dashboardPost(t *testing.T, server *httptest.Server, token, path string, body any) *http.Response {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, server.URL+path, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", server.URL)
	request.Header.Set("X-KeyMoat-CSRF", token)
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { response.Body.Close() })
	return response
}

func TestDashboardRejectsCrossOriginAndHostSpoofing(t *testing.T) {
	d, server := dashboardServer(t)
	response, err := server.Client().Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 || !bytes.Contains(page, []byte("KeyMoat")) ||
		!bytes.Contains(page, []byte(d.csrf)) {
		t.Fatal("dashboard did not render")
	}
	for _, tc := range []struct{ host, origin, token string }{
		{d.host, "https://evil.example", d.csrf},
		{d.host, server.URL, "invalid"},
		{"evil.example", server.URL, d.csrf},
	} {
		req, _ := http.NewRequest(http.MethodPost, server.URL+"/api/connection",
			strings.NewReader(`{"service":"openai","enabled":true}`))
		req.Host = tc.host
		req.Header.Set("Origin", tc.origin)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-KeyMoat-CSRF", tc.token)
		res, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusForbidden {
			t.Fatalf("unsafe request accepted: %d", res.StatusCode)
		}
	}
	enabled, err := d.store.enabledService("codex", openAIConnection)
	if err != nil || enabled {
		t.Fatalf("cross-origin request changed state: %v, %v", enabled, err)
	}
}

func TestDashboardConnectionAndRepositoryControls(t *testing.T) {
	d, server := dashboardServer(t)
	d.openAICredential = func() (bool, error) { return false, nil }
	res := dashboardPost(t, server, d.csrf, "/api/connection",
		map[string]any{"service": "openai", "enabled": true})
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("enabled connection without key: %d", res.StatusCode)
	}
	d.openAICredential = func() (bool, error) { return true, nil }
	res = dashboardPost(t, server, d.csrf, "/api/connection",
		map[string]any{"service": "openai", "enabled": true})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("failed to enable model: %d", res.StatusCode)
	}
	res = dashboardPost(t, server, d.csrf, "/api/repository",
		map[string]any{"name": "Owner/Repo", "enabled": true})
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("granted repository before GitHub connection: %d", res.StatusCode)
	}
	res = dashboardPost(t, server, d.csrf, "/api/connection",
		map[string]any{"service": "github", "enabled": true})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("failed to enable GitHub: %d", res.StatusCode)
	}
	res = dashboardPost(t, server, d.csrf, "/api/repository",
		map[string]any{"name": "Owner/Repo", "enabled": true})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("failed to grant repo: %d", res.StatusCode)
	}
	status, err := server.Client().Get(server.URL + "/api/status")
	if err != nil {
		t.Fatal(err)
	}
	defer status.Body.Close()
	var data struct {
		Connections  []string `json:"connections"`
		Repositories []string `json:"repositories"`
	}
	if err := json.NewDecoder(status.Body).Decode(&data); err != nil ||
		len(data.Connections) != 2 || len(data.Repositories) != 1 ||
		data.Repositories[0] != "owner/repo" {
		t.Fatalf("status: %+v, %v", data, err)
	}
	res = dashboardPost(t, server, d.csrf, "/api/repository",
		map[string]any{"name": "Owner/Repo", "enabled": false})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("failed to revoke repo: %d", res.StatusCode)
	}
}

func TestDashboardTaskLifecycleAndCancellation(t *testing.T) {
	d, server := dashboardServer(t)
	if err := d.store.setService("codex", openAIConnection, true); err != nil {
		t.Fatal(err)
	}
	folder := t.TempDir()
	started := make(chan struct{})
	d.runCodex = func(ctx context.Context, args []string, output io.Writer) error {
		if !strings.Contains(strings.Join(args, " "), "example-model") {
			t.Error("model argument missing")
		}
		output.Write([]byte("task started"))
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}
	task := map[string]any{"folder": folder, "model": "example-model", "prompt": "inspect the project"}
	res := dashboardPost(t, server, d.csrf, "/api/run", task)
	if res.StatusCode != 200 {
		t.Fatalf("task not started: %d", res.StatusCode)
	}
	<-started
	res = dashboardPost(t, server, d.csrf, "/api/run", task)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("concurrent task accepted: %d", res.StatusCode)
	}
	res = dashboardPost(t, server, d.csrf, "/api/cancel", map[string]any{})
	if res.StatusCode != 200 {
		t.Fatalf("cancel failed: %d", res.StatusCode)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		d.mu.Lock()
		running := d.active
		d.mu.Unlock()
		if !running {
			break
		}
		time.Sleep(time.Millisecond)
	}
	d.mu.Lock()
	running := d.active
	d.mu.Unlock()
	if running {
		t.Fatal("task remained active after cancellation")
	}
	status, err := server.Client().Get(server.URL + "/api/run")
	if err != nil {
		t.Fatal(err)
	}
	defer status.Body.Close()
	body, _ := io.ReadAll(status.Body)
	if !bytes.Contains(body, []byte("task started")) || !bytes.Contains(body, []byte("canceled")) {
		t.Fatalf("task state missing: %s", body)
	}
}

func TestDashboardFolderSelectionAndMalformedRequest(t *testing.T) {
	d, server := dashboardServer(t)
	folder := t.TempDir()
	d.pickFolder = func() (string, error) { return folder, nil }
	res := dashboardPost(t, server, d.csrf, "/api/folder", map[string]any{})
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 || !bytes.Contains(body, []byte(filepath.Base(folder))) {
		t.Fatalf("folder picker: %d %s", res.StatusCode, body)
	}
	d.pickFolder = func() (string, error) { return "", errors.New("cancelled") }
	res = dashboardPost(t, server, d.csrf, "/api/folder", map[string]any{})
	if res.StatusCode != 400 {
		t.Fatalf("cancelled picker: %d", res.StatusCode)
	}
	res = dashboardPost(t, server, d.csrf, "/api/connection",
		map[string]any{"service": "openai", "enabled": true, "extra": true})
	if res.StatusCode != 400 {
		t.Fatalf("unknown JSON field accepted: %d", res.StatusCode)
	}
}

func TestDashboardAssetAndDeniedOperations(t *testing.T) {
	d, server := dashboardServer(t)
	for _, asset := range []string{"/logo", "/style.css", "/app.js"} {
		res, err := server.Client().Get(server.URL + asset)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != 200 || len(body) == 0 || res.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("asset %s: %d %d", asset, res.StatusCode, len(body))
		}
	}
	for _, tc := range []struct {
		path string
		body any
		code int
	}{
		{"/api/connection", map[string]any{"service": "other", "enabled": true}, 400},
		{"/api/repository", map[string]any{"name": "bad/repo/extra", "enabled": true}, 400},
		{"/api/repository", map[string]any{"name": "owner/repo", "enabled": true}, 409},
		{"/api/run", map[string]any{"folder": t.TempDir(), "model": "model", "prompt": "task"}, 409},
		{"/api/run", map[string]any{"folder": t.TempDir(), "model": "model", "prompt": ""}, 400},
		{"/api/cancel", map[string]any{}, 409},
	} {
		res := dashboardPost(t, server, d.csrf, tc.path, tc.body)
		if res.StatusCode != tc.code {
			t.Fatalf("%s expected %d, got %d", tc.path, tc.code, res.StatusCode)
		}
	}
	d.openAICredential = func() (bool, error) { return false, errors.New("keychain offline") }
	d.gitHubCredential = func() (bool, error) { return false, errors.New("keychain offline") }
	res, err := server.Client().Get(server.URL + "/api/status")
	if err != nil {
		t.Fatal(err)
	}
	var status map[string]any
	if err := json.NewDecoder(res.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if status["openai_credential"] != false || status["github_credential"] != false {
		t.Fatalf("credential lookup error not reported as absent: %#v", status)
	}
}

func TestDashboardHostStateCorruptionFailsClosed(t *testing.T) {
	d, server := dashboardServer(t)
	if err := d.store.setService("codex", openAIConnection, true); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(d.store.path, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	res, err := server.Client().Get(server.URL + "/api/status")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 503 {
		t.Fatalf("corrupt state status: %d", res.StatusCode)
	}
	res = dashboardPost(t, server, d.csrf, "/api/run",
		map[string]any{"folder": t.TempDir(), "model": "test", "prompt": "task"})
	if res.StatusCode != 409 {
		t.Fatalf("corrupt state allowed task: %d", res.StatusCode)
	}
	res = dashboardPost(t, server, d.csrf, "/api/connection",
		map[string]any{"service": "openai", "enabled": false})
	if res.StatusCode != 503 {
		t.Fatalf("corrupt state accepted update: %d", res.StatusCode)
	}
}

func TestDashboardRejectsMalformedJSONAndUnsafeFolder(t *testing.T) {
	d, server := dashboardServer(t)
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/api/connection",
		strings.NewReader(`{"service":"openai"}{"enabled":true}`))
	req.Header.Set("Origin", server.URL)
	req.Header.Set("X-KeyMoat-CSRF", d.csrf)
	req.Header.Set("Content-Type", "application/json")
	res, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 400 {
		t.Fatalf("multiple JSON objects accepted: %d", res.StatusCode)
	}
	d.pickFolder = func() (string, error) {
		return chooseProjectFolderWith(func() ([]byte, error) { return nil, errors.New("cancel") })
	}
	res = dashboardPost(t, server, d.csrf, "/api/folder", map[string]any{})
	if res.StatusCode != 400 {
		t.Fatalf("cancelled folder accepted: %d", res.StatusCode)
	}
	if _, err := chooseProjectFolderWith(func() ([]byte, error) { return []byte("/missing/keymoat"), nil }); err == nil {
		t.Fatal("missing picked folder accepted")
	}
	valid := t.TempDir()
	if folder, err := chooseProjectFolderWith(func() ([]byte, error) { return []byte(valid + "\n"), nil }); err != nil || folder == "" {
		t.Fatalf("valid picked folder failed: %v", err)
	}
}

func TestDashboardOutputIsBounded(t *testing.T) {
	var output boundedOutput
	if _, err := output.Write(bytes.Repeat([]byte("a"), 70<<10)); err != nil {
		t.Fatal(err)
	}
	if len(output.String()) != 64<<10 {
		t.Fatalf("task output size: %d", len(output.String()))
	}
}

func TestDashboardServerShutdown(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	store := connectionStore{path: filepath.Join(t.TempDir(), "connections.json")}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serveDashboard(ctx, listener, store) }()
	deadline := time.Now().Add(time.Second)
	var response *http.Response
	for time.Now().Before(deadline) {
		response, err = http.Get("http://" + listener.Addr().String())
		if err == nil {
			response.Body.Close()
			break
		}
		time.Sleep(time.Millisecond)
	}
	if err != nil {
		cancel()
		t.Fatalf("dashboard failed to start: %v", err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("dashboard shutdown: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("dashboard did not stop")
	}
}

func TestDashboardShutdownCancelsActiveTask(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	store := connectionStore{path: filepath.Join(t.TempDir(), "connections.json")}
	if err := store.setService("codex", openAIConnection, true); err != nil {
		t.Fatal(err)
	}
	d, err := newDashboard(store, listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	stopped := make(chan struct{})
	d.runCodex = func(ctx context.Context, args []string, output io.Writer) error {
		close(started)
		<-ctx.Done()
		close(stopped)
		return ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serveDashboardInstance(ctx, listener, d) }()
	base := "http://" + listener.Addr().String()
	var response *http.Response
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		response, err = http.Get(base)
		if err == nil {
			response.Body.Close()
			break
		}
		time.Sleep(time.Millisecond)
	}
	if err != nil {
		cancel()
		t.Fatalf("server did not start: %v", err)
	}
	request, _ := json.Marshal(map[string]string{"folder": t.TempDir(), "model": "test", "prompt": "task"})
	req, _ := http.NewRequest(http.MethodPost, base+"/api/run", bytes.NewReader(request))
	req.Header.Set("Origin", base)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-KeyMoat-CSRF", d.csrf)
	response, err = http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		cancel()
		t.Fatalf("task did not start: %d", response.StatusCode)
	}
	<-started
	cancel()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("active task did not receive cancellation")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("server shutdown: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("server stopped before task cleanup")
	}
}

func TestDashboardRunValidationAndFailureResult(t *testing.T) {
	d, server := dashboardServer(t)
	if err := d.store.setService("codex", openAIConnection, true); err != nil {
		t.Fatal(err)
	}
	for _, request := range []map[string]any{
		{"folder": "/missing/keymoat", "model": "model", "prompt": "task"},
		{"folder": t.TempDir(), "model": strings.Repeat("x", 129), "prompt": "task"},
		{"folder": t.TempDir(), "model": "model", "prompt": strings.Repeat("x", 4001)},
	} {
		res := dashboardPost(t, server, d.csrf, "/api/run", request)
		if res.StatusCode != http.StatusBadRequest {
			t.Fatalf("invalid run input accepted: %d", res.StatusCode)
		}
	}
	current, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	res := dashboardPost(t, server, d.csrf, "/api/run",
		map[string]any{"folder": current.HomeDir, "model": "model", "prompt": "task"})
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("host home was accepted as project: %d", res.StatusCode)
	}
	done := make(chan struct{})
	d.runCodex = func(ctx context.Context, args []string, output io.Writer) error {
		output.Write([]byte("simulated task output"))
		defer close(done)
		return errors.New("simulated failure")
	}
	res = dashboardPost(t, server, d.csrf, "/api/run",
		map[string]any{"folder": t.TempDir(), "model": "test-model", "prompt": "test task"})
	if res.StatusCode != 200 {
		t.Fatalf("valid task not started: %d", res.StatusCode)
	}
	<-done
	status, err := server.Client().Get(server.URL + "/api/run")
	if err != nil {
		t.Fatal(err)
	}
	defer status.Body.Close()
	body, _ := io.ReadAll(status.Body)
	if !bytes.Contains(body, []byte("simulated task output")) ||
		!bytes.Contains(body, []byte("simulated failure")) {
		t.Fatalf("failure was not visible: %s", body)
	}
}

func TestDashboardRepositoryFailsClosedOnCorruptState(t *testing.T) {
	d, server := dashboardServer(t)
	if err := d.store.setService("codex", githubConnection, true); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(d.store.path, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, enabled := range []bool{true, false} {
		res := dashboardPost(t, server, d.csrf, "/api/repository",
			map[string]any{"name": "owner/repo", "enabled": enabled})
		if enabled && res.StatusCode != http.StatusConflict ||
			!enabled && res.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("corrupt state repository update: enabled=%v status=%d", enabled, res.StatusCode)
		}
	}
}

func TestDashboardRejectsNonJSONAndProtectedFolder(t *testing.T) {
	d, server := dashboardServer(t)
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/api/connection", strings.NewReader("enabled=true"))
	req.Header.Set("Origin", server.URL)
	req.Header.Set("X-KeyMoat-CSRF", d.csrf)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 400 {
		t.Fatalf("non-JSON request accepted: %d", res.StatusCode)
	}
	current, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := chooseProjectFolderWith(func() ([]byte, error) {
		return []byte(current.HomeDir), nil
	}); err == nil {
		t.Fatal("host home selected as project")
	}
}
