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
	"strconv"
	"strings"
	"time"
)

const (
	modelSessionHeader = "X-KeyMoat-Session"
	maxModelRequest    = 1 << 20
	maxModelResponse   = 16 << 20
)

type modelProxy struct {
	listener   net.Listener
	server     *http.Server
	done       chan struct{}
	capability string
}

type modelProxyConfig struct {
	store      connectionStore
	credential func() ([]byte, error)
	client     *http.Client
	upstream   string
}

func newModelProxyConfig(store connectionStore) modelProxyConfig {
	return modelProxyConfig{
		store:      store,
		credential: loadOpenAICredential,
		upstream:   "https://api.openai.com/v1/responses",
		client: &http.Client{
			Timeout: 3 * time.Minute,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
			},
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return errors.New("model redirect rejected")
			},
		},
	}
}

func startModelProxy(config modelProxyConfig) (*modelProxy, error) {
	enabled, err := config.store.enabledService("codex", openAIConnection)
	if err != nil {
		return nil, errors.New("model authorization state unavailable")
	}
	if !enabled {
		return nil, errors.New("OpenAI connection is disabled for Codex")
	}
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	proxy := &modelProxy{
		listener:   listener,
		done:       make(chan struct{}),
		capability: hex.EncodeToString(random),
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

func (p *modelProxy) address() string {
	return p.listener.Addr().String()
}

func (p *modelProxy) port() int {
	return p.listener.Addr().(*net.TCPAddr).Port
}

func (p *modelProxy) baseURL() string {
	return "http://127.0.0.1:" + strconv.Itoa(p.port()) + "/v1"
}

func (p *modelProxy) Close() {
	p.server.Close()
	<-p.done
}

func (config modelProxyConfig) handler(capability string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != http.MethodPost || r.URL.Path != "/v1/responses" {
			http.Error(w, "unsupported model operation", http.StatusNotFound)
			return
		}
		if subtle.ConstantTimeCompare([]byte(r.Header.Get(modelSessionHeader)), []byte(capability)) != 1 {
			http.Error(w, "unauthorized model session", http.StatusForbidden)
			return
		}
		enabled, err := config.store.enabledService("codex", openAIConnection)
		if err != nil || !enabled {
			http.Error(w, "model connection disabled", http.StatusForbidden)
			return
		}
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			http.Error(w, "JSON required", http.StatusUnsupportedMediaType)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, maxModelRequest+1))
		if err != nil || len(body) > maxModelRequest || !json.Valid(body) {
			http.Error(w, "invalid model request", http.StatusBadRequest)
			return
		}
		token, err := config.credential()
		if err != nil || len(token) == 0 {
			http.Error(w, "model credential unavailable", http.StatusServiceUnavailable)
			return
		}
		defer clearBytes(token)
		upstream, err := http.NewRequestWithContext(r.Context(), http.MethodPost, config.upstream, bytes.NewReader(body))
		if err != nil {
			http.Error(w, "model request unavailable", http.StatusBadGateway)
			return
		}
		upstream.Header.Set("Authorization", "Bearer "+string(token))
		upstream.Header.Set("Content-Type", "application/json")
		upstream.Header.Set("Accept", "text/event-stream, application/json")
		response, err := config.client.Do(upstream)
		if err != nil {
			http.Error(w, "model upstream unavailable", http.StatusBadGateway)
			return
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			http.Error(w, fmt.Sprintf("model upstream HTTP %d", response.StatusCode), http.StatusBadGateway)
			return
		}
		data, err := io.ReadAll(io.LimitReader(response.Body, maxModelResponse+1))
		if err != nil || len(data) > maxModelResponse || bytes.Contains(data, token) {
			http.Error(w, "model response rejected", http.StatusBadGateway)
			return
		}
		contentType := response.Header.Get("Content-Type")
		switch {
		case strings.HasPrefix(contentType, "text/event-stream"):
			w.Header().Set("Content-Type", "text/event-stream")
		case strings.HasPrefix(contentType, "application/json"):
			w.Header().Set("Content-Type", "application/json")
		default:
			http.Error(w, "unsupported model response", http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write(data)
	}
}
