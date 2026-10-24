package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
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
	active   sync.WaitGroup
}

func startBroker(home, agent, folder string, github githubService) (*broker, string, error) {
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
	limit := make(chan struct{}, 8)
	go func() {
		defer close(b.done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			select {
			case limit <- struct{}{}:
				b.active.Add(1)
				go func() {
					defer b.active.Done()
					defer func() { <-limit }()
					serveBrokerConnection(conn, agent, folder, github)
				}()
			default:
				conn.Close()
			}
		}
	}()
	return b, path, nil
}

func (b *broker) Close() {
	b.listener.Close()
	<-b.done
	b.active.Wait()
}

func serveBrokerConnection(conn net.Conn, agent, folder string, github githubService) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(12 * time.Second))
	var request struct {
		Op string `json:"op"`
	}
	decoder := json.NewDecoder(io.LimitReader(conn, 4096))
	if err := decoder.Decode(&request); err != nil {
		json.NewEncoder(conn).Encode(map[string]string{"error": "invalid request"})
		return
	}
	switch request.Op {
	case "status":
		enabled, err := github.store.enabled(agent)
		if err != nil {
			json.NewEncoder(conn).Encode(map[string]string{"error": "authorization state unavailable"})
			return
		}
		connections := []string{}
		if enabled {
			connections = append(connections, githubConnection)
		}
		json.NewEncoder(conn).Encode(statusResponse{agent, folder, connections})
	case "github_identity":
		identity, err := github.identity(context.Background(), agent)
		if err != nil {
			json.NewEncoder(conn).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(conn).Encode(identity)
	default:
		json.NewEncoder(conn).Encode(map[string]string{"error": "unsupported request"})
	}
}

func readStatus(path string) (statusResponse, error) {
	var status statusResponse
	if err := brokerCall(path, "status", &status); err != nil {
		return status, err
	}
	if status.Agent == "" || status.Folder == "" {
		return status, errors.New("invalid broker response")
	}
	return status, nil
}

func readIdentity(path string) (githubIdentity, error) {
	var identity githubIdentity
	if err := brokerCall(path, "github_identity", &identity); err != nil {
		return identity, err
	}
	if identity.Login == "" || identity.ID <= 0 {
		return identity, errors.New("invalid broker response")
	}
	return identity, nil
}

func brokerCall(path, op string, result any) error {
	if path == "" {
		return errors.New("not running inside a 1024 sandbox")
	}
	conn, err := net.DialTimeout("unix", path, time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(12 * time.Second))
	if err := json.NewEncoder(conn).Encode(map[string]string{"op": op}); err != nil {
		return err
	}
	var raw json.RawMessage
	if err := json.NewDecoder(io.LimitReader(conn, 4096)).Decode(&raw); err != nil {
		return err
	}
	var failure struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(raw, &failure); err != nil {
		return err
	}
	if failure.Error != "" {
		return errors.New(failure.Error)
	}
	return json.Unmarshal(raw, result)
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
