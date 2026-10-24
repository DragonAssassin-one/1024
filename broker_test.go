package main

import (
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"
)

func TestBrokerStatusOnly(t *testing.T) {
	home := t.TempDir()
	broker, path, err := startBroker(home, "dsh", "/project")
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
