package main

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

type anthropicProxy struct {
	listener   net.Listener
	server     *http.Server
	done       chan struct{}
	capability string
}

type anthropicProxyConfig struct {
	store      connectionStore
	credential func() ([]byte, error)
	client     *http.Client
	upstream   string
}

func newAnthropicProxyConfig(store connectionStore) anthropicProxyConfig {
	return anthropicProxyConfig{
		store:      store,
		credential: loadAnthropicCredential,
		upstream:   "https://api.anthropic.com/v1/messages",
		client: &http.Client{
			Timeout: 3 * time.Minute,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
			},
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return errors.New("Anthropic redirect rejected")
			},
		},
	}
}

func startAnthropicProxy(config anthropicProxyConfig) (*anthropicProxy, error) {
	enabled, err := config.store.enabledService("claude", anthropicConnection)
	if err != nil || !enabled {
		return nil, errors.New("Anthropic connection is disabled for Claude")
	}
	seed := make([]byte, 32)
	if _, err := rand.Read(seed); err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	proxy := &anthropicProxy{
		listener:   listener,
		done:       make(chan struct{}),
		capability: hex.EncodeToString(seed),
	}
	proxy.server = &http.Server{
		Handler:           http.HandlerFunc(config.handler(proxy.capability)),
		ReadHeaderTimeout: 3 * time.Second,
		IdleTimeout:       15 * time.Second,
		WriteTimeout:      3 * time.Minute,
		MaxHeaderBytes:    8 << 10,
	}
	go func() {
		defer close(proxy.done)
		proxy.server.Serve(listener)
	}()
	return proxy, nil
}

func (p *anthropicProxy) port() int {
	return p.listener.Addr().(*net.TCPAddr).Port
}

func (p *anthropicProxy) baseURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", p.port())
}

func (p *anthropicProxy) Close() {
	p.server.Close()
	<-p.done
}

func (config anthropicProxyConfig) handler(capability string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != http.MethodPost || r.URL.Path != "/v1/messages" {
			http.Error(w, "unsupported Anthropic operation", http.StatusNotFound)
			return
		}
		session := r.Header.Get("X-Api-Key")
		if subtle.ConstantTimeCompare([]byte(session), []byte(capability)) != 1 {
			http.Error(w, "unauthorized Anthropic session", http.StatusForbidden)
			return
		}
		enabled, err := config.store.enabledService("claude", anthropicConnection)
		if err != nil || !enabled {
			http.Error(w, "Anthropic connection disabled", http.StatusForbidden)
			return
		}
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			http.Error(w, "JSON required", http.StatusUnsupportedMediaType)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, maxModelRequest+1))
		if err != nil || len(body) > maxModelRequest || !json.Valid(body) {
			http.Error(w, "invalid Anthropic request", http.StatusBadRequest)
			return
		}
		token, err := config.credential()
		if err != nil || len(token) == 0 {
			http.Error(w, "Anthropic credential unavailable", http.StatusServiceUnavailable)
			return
		}
		defer clearBytes(token)
		upstream, err := http.NewRequestWithContext(r.Context(), http.MethodPost, config.upstream, bytes.NewReader(body))
		if err != nil {
			http.Error(w, "Anthropic request unavailable", http.StatusBadGateway)
			return
		}
		upstream.Header.Set("x-api-key", string(token))
		upstream.Header.Set("anthropic-version", "2023-06-01")
		upstream.Header.Set("Content-Type", "application/json")
		upstream.Header.Set("Accept", "text/event-stream, application/json")
		for _, header := range []string{"anthropic-beta", "x-stainless-retry-count"} {
			if value := r.Header.Get(header); value != "" {
				upstream.Header.Set(header, value)
			}
		}
		response, err := config.client.Do(upstream)
		if err != nil {
			http.Error(w, "Anthropic upstream unavailable", http.StatusBadGateway)
			return
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			http.Error(w, fmt.Sprintf("Anthropic upstream HTTP %d", response.StatusCode), http.StatusBadGateway)
			return
		}
		contentType := response.Header.Get("Content-Type")
		switch {
		case strings.HasPrefix(contentType, "text/event-stream"):
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_ = streamModelResponse(w, response.Body, token, maxModelResponse)
		case strings.HasPrefix(contentType, "application/json"):
			data, err := io.ReadAll(io.LimitReader(response.Body, maxModelResponse+1))
			if err != nil || len(data) > maxModelResponse || bytes.Contains(data, token) {
				http.Error(w, "Anthropic response rejected", http.StatusBadGateway)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write(data)
		default:
			http.Error(w, "unsupported Anthropic response", http.StatusBadGateway)
		}
	}
}
