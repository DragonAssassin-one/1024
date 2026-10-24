package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

//go:embed ui/* assets/keymoat-mark.png
var dashboardFiles embed.FS

type dashboard struct {
	store            connectionStore
	openAICredential func() (bool, error)
	gitHubCredential func() (bool, error)
	pickFolder       func() (string, error)
	runCodex         func(context.Context, []string, io.Writer) error
	csrf             string
	host             string
	mu               sync.Mutex
	active           bool
	cancel           context.CancelFunc
	taskDone         chan struct{}
	output           *boundedOutput
	result           string
}

type boundedOutput struct {
	mu   sync.Mutex
	data []byte
}

func (o *boundedOutput) Write(data []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.data = append(o.data, data...)
	if len(o.data) > 64<<10 {
		o.data = append([]byte(nil), o.data[len(o.data)-(64<<10):]...)
	}
	return len(data), nil
}

func (o *boundedOutput) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return string(o.data)
}

func newDashboard(store connectionStore, host string) (*dashboard, error) {
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		return nil, err
	}
	d := &dashboard{
		store: store, host: host, csrf: hex.EncodeToString(token),
		openAICredential: openAICredentialExists,
		gitHubCredential: githubCredentialExists,
		pickFolder:       chooseProjectFolder,
	}
	d.runCodex = func(ctx context.Context, args []string, output io.Writer) error {
		return runCodexTask(args, defaultConnectionStore, findCodexBinary,
			newModelProxyConfig, ctx, output)
	}
	return d, nil
}

func chooseProjectFolder() (string, error) {
	return chooseProjectFolderWith(func() ([]byte, error) {
		command := exec.Command("/usr/bin/osascript", "-e",
			`POSIX path of (choose folder with prompt "Select a KeyMoat project folder")`)
		return command.Output()
	})
}

func chooseProjectFolderWith(pick func() ([]byte, error)) (string, error) {
	output, err := pick()
	if err != nil {
		return "", errors.New("folder selection cancelled")
	}
	folder, err := canonicalDir(strings.TrimSpace(string(output)))
	if err != nil {
		return "", err
	}
	if err := validateProject(folder); err != nil {
		return "", err
	}
	return folder, nil
}

func (d *dashboard) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", d.index)
	mux.HandleFunc("GET /logo", func(w http.ResponseWriter, r *http.Request) {
		data, err := dashboardFiles.ReadFile("assets/keymoat-mark.png")
		if err != nil {
			http.Error(w, "asset unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write(data)
	})
	mux.HandleFunc("GET /style.css", d.asset("ui/style.css", "text/css; charset=utf-8"))
	mux.HandleFunc("GET /app.js", d.asset("ui/app.js", "text/javascript; charset=utf-8"))
	mux.HandleFunc("GET /api/status", d.status)
	mux.HandleFunc("GET /api/run", d.runStatus)
	mux.HandleFunc("POST /api/folder", d.folder)
	mux.HandleFunc("POST /api/connection", d.connection)
	mux.HandleFunc("POST /api/repository", d.repository)
	mux.HandleFunc("POST /api/run", d.startRun)
	mux.HandleFunc("POST /api/cancel", d.cancelRun)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self'; base-uri 'none'; form-action 'none'")
		if r.Host != d.host {
			http.Error(w, "invalid host", http.StatusForbidden)
			return
		}
		if r.Method == http.MethodPost {
			if r.Header.Get("Origin") != "http://"+d.host ||
				subtle.ConstantTimeCompare([]byte(r.Header.Get("X-KeyMoat-CSRF")), []byte(d.csrf)) != 1 {
				http.Error(w, "invalid request origin", http.StatusForbidden)
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}

func (d *dashboard) asset(path, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := dashboardFiles.ReadFile(path)
		if err != nil {
			http.Error(w, "asset unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.Write(data)
	}
}

func (d *dashboard) index(w http.ResponseWriter, r *http.Request) {
	content, err := dashboardFiles.ReadFile("ui/index.html")
	if err != nil {
		http.Error(w, "page unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	template.Must(template.New("page").Parse(string(content))).Execute(w, struct{ CSRF string }{d.csrf})
}

func dashboardJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(value)
}

func decodeDashboardRequest(r *http.Request, value any) error {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		return errors.New("JSON required")
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, 8193))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("invalid request body")
	}
	return nil
}

func dashboardError(w http.ResponseWriter, err error, code int) {
	w.WriteHeader(code)
	dashboardJSON(w, map[string]string{"error": err.Error()})
}

func (d *dashboard) status(w http.ResponseWriter, r *http.Request) {
	connections, repositories, err := d.store.status("codex")
	if err != nil {
		dashboardError(w, errors.New("authorization state unavailable"), http.StatusServiceUnavailable)
		return
	}
	openAIKey, openAIErr := d.openAICredential()
	githubKey, githubErr := d.gitHubCredential()
	dashboardJSON(w, map[string]any{
		"agent": "codex", "connections": connections, "repositories": repositories,
		"openai_credential": openAIErr == nil && openAIKey,
		"github_credential": githubErr == nil && githubKey,
	})
}

func (d *dashboard) folder(w http.ResponseWriter, r *http.Request) {
	folder, err := d.pickFolder()
	if err != nil {
		dashboardError(w, err, http.StatusBadRequest)
		return
	}
	dashboardJSON(w, map[string]string{"folder": folder})
}

func (d *dashboard) connection(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Service string `json:"service"`
		Enabled bool   `json:"enabled"`
	}
	if err := decodeDashboardRequest(r, &request); err != nil {
		dashboardError(w, err, http.StatusBadRequest)
		return
	}
	var exists func() (bool, error)
	switch request.Service {
	case openAIConnection:
		exists = d.openAICredential
	case githubConnection:
		exists = d.gitHubCredential
	default:
		dashboardError(w, errors.New("unsupported connection"), http.StatusBadRequest)
		return
	}
	if request.Enabled {
		ok, err := exists()
		if err != nil || !ok {
			dashboardError(w, errors.New("credential unavailable; add it from the host terminal"), http.StatusConflict)
			return
		}
	}
	if err := d.store.setService("codex", request.Service, request.Enabled); err != nil {
		dashboardError(w, errors.New("authorization update failed"), http.StatusServiceUnavailable)
		return
	}
	dashboardJSON(w, map[string]bool{"enabled": request.Enabled})
}

func (d *dashboard) repository(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
	}
	if err := decodeDashboardRequest(r, &request); err != nil {
		dashboardError(w, err, http.StatusBadRequest)
		return
	}
	name, err := canonicalRepository(request.Name)
	if err != nil {
		dashboardError(w, err, http.StatusBadRequest)
		return
	}
	if request.Enabled {
		enabled, err := d.store.enabled("codex")
		if err != nil || !enabled {
			dashboardError(w, errors.New("enable GitHub first"), http.StatusConflict)
			return
		}
	}
	if err := d.store.setRepository("codex", name, request.Enabled); err != nil {
		dashboardError(w, errors.New("repository authorization failed"), http.StatusServiceUnavailable)
		return
	}
	dashboardJSON(w, map[string]string{"repository": name})
}

func (d *dashboard) startRun(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Folder string `json:"folder"`
		Model  string `json:"model"`
		Prompt string `json:"prompt"`
	}
	if err := decodeDashboardRequest(r, &request); err != nil {
		dashboardError(w, err, http.StatusBadRequest)
		return
	}
	if len(request.Model) > 128 || len(request.Prompt) > 4000 || strings.TrimSpace(request.Prompt) == "" {
		dashboardError(w, errors.New("invalid model or prompt"), http.StatusBadRequest)
		return
	}
	folder, err := canonicalDir(request.Folder)
	if err != nil {
		dashboardError(w, errors.New("folder unavailable"), http.StatusBadRequest)
		return
	}
	if err := validateProject(folder); err != nil {
		dashboardError(w, err, http.StatusBadRequest)
		return
	}
	enabled, err := d.store.enabledService("codex", openAIConnection)
	if err != nil || !enabled {
		dashboardError(w, errors.New("enable OpenAI first"), http.StatusConflict)
		return
	}
	d.mu.Lock()
	if d.active {
		d.mu.Unlock()
		dashboardError(w, errors.New("Codex task already running"), http.StatusConflict)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	d.cancel, d.active, d.result = cancel, true, ""
	d.output = &boundedOutput{}
	d.taskDone = make(chan struct{})
	output := d.output
	done := d.taskDone
	d.mu.Unlock()
	args := []string{"--folder", folder, "--model", request.Model, "--", request.Prompt}
	go func() {
		defer close(done)
		err := d.runCodex(ctx, args, output)
		d.mu.Lock()
		d.active = false
		if err != nil {
			d.result = "Task ended: " + err.Error()
		} else {
			d.result = "Task complete"
		}
		d.cancel = nil
		d.mu.Unlock()
		cancel()
	}()
	dashboardJSON(w, map[string]bool{"started": true})
}

func (d *dashboard) runStatus(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	active, result := d.active, d.result
	output := d.output
	d.mu.Unlock()
	text := ""
	if output != nil {
		text = output.String()
	}
	dashboardJSON(w, map[string]any{"running": active, "result": result, "output": text})
}

func (d *dashboard) cancelRun(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	cancel := d.cancel
	d.mu.Unlock()
	if cancel == nil {
		dashboardError(w, errors.New("no active task"), http.StatusConflict)
		return
	}
	cancel()
	dashboardJSON(w, map[string]bool{"cancelled": true})
}

func runDashboard() error {
	store, err := defaultConnectionStore()
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer listener.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return serveDashboard(ctx, listener, store)
}

func serveDashboard(ctx context.Context, listener net.Listener, store connectionStore) error {
	dashboard, err := newDashboard(store, listener.Addr().String())
	if err != nil {
		return err
	}
	return serveDashboardInstance(ctx, listener, dashboard)
}

func serveDashboardInstance(ctx context.Context, listener net.Listener, dashboard *dashboard) error {
	server := &http.Server{Handler: dashboard.handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		dashboard.mu.Lock()
		cancel := dashboard.cancel
		done := dashboard.taskDone
		dashboard.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		if done != nil {
			select {
			case <-done:
			case <-time.After(3 * time.Second):
			}
		}
		shutdown, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		server.Shutdown(shutdown)
	}()
	fmt.Printf("KeyMoat control: http://%s\n", listener.Addr())
	err := server.Serve(listener)
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}
