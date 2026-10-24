package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"
)

const brokerSocketEnv = "SANDBOX_BROKER_SOCKET"

type statusResponse struct {
	Agent       string   `json:"agent"`
	Folder      string   `json:"folder"`
	Connections []string `json:"connections"`
}

type broker struct {
	listener net.Listener
	done     chan struct{}
}

func startBroker(home, agent, folder string) (*broker, string, error) {
	path := filepath.Join(home, "broker.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, "", err
	}
	if err := os.Chmod(path, 0600); err != nil {
		listener.Close()
		return nil, "", err
	}
	b := &broker{listener: listener, done: make(chan struct{})}
	go func() {
		defer close(b.done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			serveBrokerConnection(conn, statusResponse{agent, folder, []string{}})
		}
	}()
	return b, path, nil
}

func (b *broker) Close() {
	b.listener.Close()
	<-b.done
}

func serveBrokerConnection(conn net.Conn, status statusResponse) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	var request struct {
		Op string `json:"op"`
	}
	decoder := json.NewDecoder(io.LimitReader(conn, 4096))
	if err := decoder.Decode(&request); err != nil || request.Op != "status" {
		json.NewEncoder(conn).Encode(map[string]string{"error": "unsupported request"})
		return
	}
	json.NewEncoder(conn).Encode(status)
}

func readStatus(path string) (statusResponse, error) {
	var status statusResponse
	if path == "" {
		return status, errors.New("not running inside a 1024 sandbox")
	}
	conn, err := net.DialTimeout("unix", path, time.Second)
	if err != nil {
		return status, err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := json.NewEncoder(conn).Encode(map[string]string{"op": "status"}); err != nil {
		return status, err
	}
	if err := json.NewDecoder(io.LimitReader(conn, 4096)).Decode(&status); err != nil {
		return status, err
	}
	if status.Agent == "" || status.Folder == "" {
		return status, errors.New("invalid broker response")
	}
	return status, nil
}

func printStatus() error {
	status, err := readStatus(os.Getenv(brokerSocketEnv))
	if err != nil {
		return err
	}
	fmt.Printf("Agent       %s\nFolder      %s\nConnections %d\n",
		status.Agent, status.Folder, len(status.Connections))
	return nil
}
