//go:build ignore

package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

func main() {
	if len(os.Args) != 2 {
		fail(fmt.Errorf("usage: dashboard-smoke BINARY"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[1], "dashboard")
	stdout, err := command.StdoutPipe()
	if err != nil {
		fail(err)
	}
	if err := command.Start(); err != nil {
		fail(err)
	}
	defer func() {
		command.Process.Kill()
		command.Wait()
	}()
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || !strings.HasPrefix(line, "KeyMoat control: http://127.0.0.1:") {
		fail(fmt.Errorf("dashboard did not bind loopback: %q: %v", line, err))
	}
	base := strings.TrimSpace(strings.TrimPrefix(line, "KeyMoat control: "))
	client := &http.Client{Timeout: 2 * time.Second}
	for _, path := range []string{"/", "/style.css", "/app.js", "/logo"} {
		response, err := client.Get(base + path)
		if err != nil {
			fail(err)
		}
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if response.StatusCode != 200 {
			fail(fmt.Errorf("dashboard %s returned HTTP %d", path, response.StatusCode))
		}
	}
	if err := command.Process.Signal(os.Interrupt); err != nil {
		fail(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			fail(err)
		}
	case <-ctx.Done():
		fail(ctx.Err())
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
