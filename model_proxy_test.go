package main

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestModelProxyAuthorizationAndRevocation(t *testing.T) {
	store := connectionStore{path: filepath.Join(t.TempDir(), "connections.json")}
	var upstreamCalls atomic.Int32
	var seenToken atomic.Value
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		seenToken.Store(r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"resp_1","output":[{"type":"message","content":[{"type":"output_text","text":"done"}]}]}`))
	}))
	defer upstream.Close()
	config := modelProxyConfig{
		store:    store,
		upstream: upstream.URL,
		client:   upstream.Client(),
		credential: func() ([]byte, error) {
			return []byte("secret-model-token"), nil
		},
	}
	if _, err := startModelProxy(config); err == nil {
		t.Fatal("proxy started without authorization")
	}
	if err := store.setService("codex", openAIConnection, true); err != nil {
		t.Fatal(err)
	}
	proxy, err := startModelProxy(config)
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	requestBody := []byte(`{"model":"test","input":"hello"}`)
	do := func(path, capability string) (*http.Response, error) {
		request, err := http.NewRequest(http.MethodPost, "http://"+proxy.address()+path, bytes.NewReader(requestBody))
		if err != nil {
			return nil, err
		}
		request.Header.Set(modelSessionHeader, capability)
		request.Header.Set("Content-Type", "application/json")
		return http.DefaultClient.Do(request)
	}
	response, err := do("/v1/responses", "wrong")
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("wrong capability status: %d", response.StatusCode)
	}
	response.Body.Close()
	response, err = do("/v1/models", proxy.capability)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("unsupported path status: %d", response.StatusCode)
	}
	response.Body.Close()
	response, err = do("/v1/responses", proxy.capability)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || !bytes.Contains(body, []byte("done")) ||
		bytes.Contains(body, []byte("secret-model-token")) {
		t.Fatalf("proxy response: %d %s", response.StatusCode, body)
	}
	if upstreamCalls.Load() != 1 || seenToken.Load() != "Bearer secret-model-token" {
		t.Fatalf("upstream authorization: %v %v", upstreamCalls.Load(), seenToken.Load())
	}
	if err := store.setService("codex", openAIConnection, false); err != nil {
		t.Fatal(err)
	}
	response, err = do("/v1/responses", proxy.capability)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("revoked proxy status: %d", response.StatusCode)
	}
	response.Body.Close()
	if upstreamCalls.Load() != 1 {
		t.Fatal("revoked request reached upstream")
	}
}

func TestModelProxyRejectsUnsafePayloads(t *testing.T) {
	store := connectionStore{path: filepath.Join(t.TempDir(), "connections.json")}
	store.setService("codex", openAIConnection, true)
	for _, tc := range []struct {
		name   string
		body   string
		header string
	}{
		{"invalid json", "{", "application/json"},
		{"wrong media", "{}", "text/plain"},
		{"oversized", `{"input":"` + strings.Repeat("x", maxModelRequest) + `"}`, "application/json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Fatal("unsafe request reached upstream")
			}))
			defer upstream.Close()
			proxy, err := startModelProxy(modelProxyConfig{
				store: store, upstream: upstream.URL, client: upstream.Client(),
				credential: func() ([]byte, error) { return []byte("token"), nil },
			})
			if err != nil {
				t.Fatal(err)
			}
			defer proxy.Close()
			request, _ := http.NewRequest(http.MethodPost, "http://"+proxy.address()+"/v1/responses",
				strings.NewReader(tc.body))
			request.Header.Set(modelSessionHeader, proxy.capability)
			request.Header.Set("Content-Type", tc.header)
			client := &http.Client{Timeout: time.Second}
			response, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode < 400 || response.StatusCode >= 500 {
				t.Fatalf("unsafe payload status: %d", response.StatusCode)
			}
			response.Body.Close()
		})
	}
}

func TestModelProxyDoesNotLeakUpstreamCredentials(t *testing.T) {
	store := connectionStore{path: filepath.Join(t.TempDir(), "connections.json")}
	if err := store.setService("codex", openAIConnection, true); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name       string
		status     int
		media      string
		body       string
		wantStatus int
	}{
		{"upstream error", 401, "application/json", `{"error":"secret-token"}`, 502},
		{"reflected token", 200, "application/json", `{"text":"secret-token"}`, 502},
		{"unsupported media", 200, "text/html", "<h1>html</h1>", 502},
		{"stream response", 200, "text/event-stream", "event: response.completed\ndata: {}\n\n", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tc.media)
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.body))
			}))
			defer upstream.Close()
			proxy, err := startModelProxy(modelProxyConfig{
				store: store, upstream: upstream.URL, client: upstream.Client(),
				credential: func() ([]byte, error) { return []byte("secret-token"), nil },
			})
			if err != nil {
				t.Fatal(err)
			}
			defer proxy.Close()
			request, _ := http.NewRequest(http.MethodPost,
				"http://"+proxy.address()+"/v1/responses", strings.NewReader(`{"input":"test"}`))
			request.Header.Set(modelSessionHeader, proxy.capability)
			request.Header.Set("Content-Type", "application/json")
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(response.Body)
			response.Body.Close()
			if response.StatusCode != tc.wantStatus || bytes.Contains(body, []byte("secret-token")) {
				t.Fatalf("proxy leaked upstream: %d %q", response.StatusCode, body)
			}
		})
	}
}

func TestModelProxyRejectsMissingCredentialAndCorruptAuthorization(t *testing.T) {
	store := connectionStore{path: filepath.Join(t.TempDir(), "connections.json")}
	store.setService("codex", openAIConnection, true)
	proxy, err := startModelProxy(modelProxyConfig{
		store: store, credential: func() ([]byte, error) { return nil, errors.New("private failure") },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	call := func() (int, string) {
		request, _ := http.NewRequest(http.MethodPost,
			"http://"+proxy.address()+"/v1/responses", strings.NewReader("{}"))
		request.Header.Set(modelSessionHeader, proxy.capability)
		request.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		return response.StatusCode, string(body)
	}
	if status, body := call(); status != 503 || strings.Contains(body, "private failure") {
		t.Fatalf("keychain error leaked: %d %s", status, body)
	}
	if err := os.WriteFile(store.path, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if status, _ := call(); status != 403 {
		t.Fatalf("corrupt authorization remained active: %d", status)
	}
}

func TestDefaultModelProxyConfigRejectsRedirect(t *testing.T) {
	config := newModelProxyConfig(connectionStore{path: filepath.Join(t.TempDir(), "connections.json")})
	if config.upstream != "https://api.openai.com/v1/responses" || config.client == nil {
		t.Fatalf("unexpected upstream: %s", config.upstream)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirected" {
			t.Error("redirect followed")
			return
		}
		http.Redirect(w, r, "/redirected", http.StatusFound)
	}))
	defer upstream.Close()
	response, err := config.client.Get(upstream.URL)
	if err == nil {
		response.Body.Close()
		t.Fatal("model client followed redirect")
	}
}

func TestModelProxyRejectsOversizedAndRedirectedResponses(t *testing.T) {
	store := connectionStore{path: filepath.Join(t.TempDir(), "connections.json")}
	store.setService("codex", openAIConnection, true)
	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
	}{
		{"oversized", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write(bytes.Repeat([]byte("x"), maxModelResponse+1))
		}},
		{"redirect", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/other", http.StatusFound)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(tc.handler)
			defer upstream.Close()
			config := newModelProxyConfig(store)
			config.upstream = upstream.URL
			config.credential = func() ([]byte, error) { return []byte("secret-token"), nil }
			proxy, err := startModelProxy(config)
			if err != nil {
				t.Fatal(err)
			}
			defer proxy.Close()
			req, _ := http.NewRequest(http.MethodPost, "http://"+proxy.address()+"/v1/responses",
				strings.NewReader("{}"))
			req.Header.Set(modelSessionHeader, proxy.capability)
			req.Header.Set("Content-Type", "application/json")
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			if res.StatusCode != 502 {
				t.Fatalf("unsafe upstream response was accepted: %d", res.StatusCode)
			}
		})
	}
}

func TestModelProxyRejectsMalformedUpstreamConfiguration(t *testing.T) {
	store := connectionStore{path: filepath.Join(t.TempDir(), "connections.json")}
	if err := store.setService("codex", openAIConnection, true); err != nil {
		t.Fatal(err)
	}
	for _, config := range []modelProxyConfig{
		{store: store, upstream: ":bad-url", client: http.DefaultClient,
			credential: func() ([]byte, error) { return []byte("token"), nil }},
		{store: store, upstream: "http://127.0.0.1:1/v1/responses", client: &http.Client{Timeout: time.Second},
			credential: func() ([]byte, error) { return []byte("token"), nil }},
		{store: store, credential: func() ([]byte, error) { return nil, nil }},
	} {
		proxy, err := startModelProxy(config)
		if err != nil {
			t.Fatal(err)
		}
		req, _ := http.NewRequest(http.MethodPost, "http://"+proxy.address()+"/v1/responses",
			strings.NewReader("{}"))
		req.Header.Set(modelSessionHeader, proxy.capability)
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			proxy.Close()
			t.Fatal(err)
		}
		if res.StatusCode < 500 {
			t.Fatalf("invalid upstream accepted: %d", res.StatusCode)
		}
		res.Body.Close()
		proxy.Close()
	}
}

func TestModelProxyFailsClosedOnCorruptStateAtStartup(t *testing.T) {
	store := connectionStore{path: filepath.Join(t.TempDir(), "connections.json")}
	if err := os.WriteFile(store.path, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if proxy, err := startModelProxy(modelProxyConfig{store: store}); err == nil {
		proxy.Close()
		t.Fatal("model proxy started with corrupt authorization state")
	}
}
