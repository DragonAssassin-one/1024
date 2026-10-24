package main

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBrokerStatusOnly(t *testing.T) {
	home := t.TempDir()
	store := connectionStore{path: filepath.Join(t.TempDir(), "connections.json")}
	broker, path, err := startBroker(home, "dsh", []string{"/project"}, githubService{store: store})
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	if path != filepath.Join(home, "broker.sock") {
		t.Fatalf("unexpected socket: %s", path)
	}
	status, err := readStatus(path)
	if err != nil || status.Agent != "dsh" || status.Folder != "/project" || len(status.Connections) != 0 {
		t.Fatalf("status: %+v, %v", status, err)
	}

	conn, err := net.DialTimeout("unix", path, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := json.NewEncoder(conn).Encode(map[string]string{"op": "execute"}); err != nil {
		t.Fatal(err)
	}
	var response map[string]string
	if err := json.NewDecoder(conn).Decode(&response); err != nil || response["error"] != "unsupported request" {
		t.Fatalf("unsupported request: %v, %+v", err, response)
	}
}

func TestStatusWithoutBroker(t *testing.T) {
	if _, err := readStatus(""); err == nil {
		t.Fatal("status outside sandbox should fail")
	}
}

func TestBrokerRevocation(t *testing.T) {
	store := connectionStore{path: filepath.Join(t.TempDir(), "connections.json")}
	reads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"login":"user","id":42,"credential":"hidden"}`))
	}))
	defer server.Close()
	service := githubService{
		store: store,
		credential: func() ([]byte, error) {
			reads++
			return []byte("test-token"), nil
		},
		client: server.Client(), endpoint: server.URL,
	}
	broker, path, err := startBroker(t.TempDir(), "codex", []string{"/project"}, service)
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	if _, err := readIdentity(path); err == nil || reads != 0 {
		t.Fatalf("disabled connection accessed credential: %v", err)
	}
	if err := store.set("codex", true); err != nil {
		t.Fatal(err)
	}
	status, err := readStatus(path)
	if err != nil || len(status.Connections) != 1 || status.Connections[0] != githubConnection {
		t.Fatalf("enabled status: %+v, %v", status, err)
	}
	identity, err := readIdentity(path)
	if err != nil || identity.Login != "user" || identity.ID != 42 || reads != 1 {
		t.Fatalf("identity: %+v, %v", identity, err)
	}
	if err := store.set("codex", false); err != nil {
		t.Fatal(err)
	}
	if _, err := readIdentity(path); err == nil || reads != 1 {
		t.Fatalf("revoked connection accessed credential: %v", err)
	}
	status, err = readStatus(path)
	if err != nil || len(status.Connections) != 0 {
		t.Fatalf("disabled status: %+v, %v", status, err)
	}
	if err := os.WriteFile(store.path, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readStatus(path); err == nil {
		t.Fatal("corrupt authorization state should fail status request")
	}
	if _, err := readIdentity(path); err == nil || reads != 1 {
		t.Fatalf("corrupt authorization state accessed credential: %v", err)
	}
}
