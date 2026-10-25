package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

func runClaude(args []string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runClaudeWithDeps(args, defaultConnectionStore, findClaudeBinary,
		newAnthropicProxyConfig, ctx)
}

func findClaudeBinary() (string, error) {
	path, err := exec.LookPath("claude")
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(path)
}

func runClaudeWithDeps(args []string, storeForUser func() (connectionStore, error),
	findBinary func() (string, error), proxyConfig func(connectionStore) anthropicProxyConfig,
	ctx context.Context) error {
	return runClaudeTask(args, storeForUser, findBinary, proxyConfig, ctx, nil)
}

func runClaudeTask(args []string, storeForUser func() (connectionStore, error),
	findBinary func() (string, error), proxyConfig func(connectionStore) anthropicProxyConfig,
	ctx context.Context, output io.Writer) error {
	if runtime.GOOS != "darwin" {
		return errors.New("this prototype requires macOS Seatbelt")
	}
	flags := flag.NewFlagSet("claude", flag.ContinueOnError)
	model := flags.String("model", "", "Anthropic model ID")
	var requested foldersFlag
	var requestedReadOnly foldersFlag
	flags.Var(&requested, "folder", "authorized writable folder")
	flags.Var(&requestedReadOnly, "read-only", "authorized read-only folder")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(requested) == 0 || *model == "" || flags.NArg() != 1 || flags.Arg(0) == "" {
		return errors.New("usage: 1024 claude --folder PATH [--read-only PATH ...] --model MODEL -- PROMPT")
	}
	if strings.ContainsAny(*model, "\r\n\"' \t") || len(*model) > 128 {
		return errors.New("invalid model ID")
	}
	folders, err := canonicalFolders(requested)
	if err != nil {
		return err
	}
	readOnly, err := canonicalFolders(requestedReadOnly)
	if err != nil {
		return err
	}
	if err := validateFolderOverlap(folders, readOnly); err != nil {
		return err
	}
	binary, err := findBinary()
	if err != nil {
		return err
	}
	store, err := storeForUser()
	if err != nil {
		return err
	}
	proxy, err := startAnthropicProxy(proxyConfig(store))
	if err != nil {
		return fmt.Errorf("Anthropic proxy: %w", err)
	}
	defer proxy.Close()
	runtimeDir := filepath.Join("/private/tmp", "claude-"+strconv.Itoa(os.Getuid()))
	info, err := os.Lstat(runtimeDir)
	if errors.Is(err, os.ErrNotExist) {
		if err = os.Mkdir(runtimeDir, 0700); err != nil {
			return err
		}
		info, err = os.Lstat(runtimeDir)
	}
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Getuid()) || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return errors.New("Claude runtime directory is not private to this user")
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	self, err = filepath.EvalSymlinks(self)
	if err != nil {
		return err
	}
	mcpConfig, err := json.Marshal(map[string]any{
		"mcpServers": map[string]any{
			"keymoat": map[string]any{"command": self, "args": []string{"mcp"}},
		},
	})
	if err != nil {
		return err
	}
	commandArgs := []string{"--bare", "--print", "--verbose", "--model", *model,
		"--output-format", "stream-json", "--no-session-persistence",
		"--dangerously-skip-permissions", "--strict-mcp-config",
		"--mcp-config", string(mcpConfig), "--", flags.Arg(0)}
	options := runOptions{
		proxyPort:         proxy.port(),
		homeRoot:          filepath.Join(filepath.Dir(store.path), "sessions"),
		extraRead:         []string{runtimeDir},
		extraReadMetadata: []string{"/tmp"},
		ctx:               ctx,
		output:            output,
		store:             &store,
		nonInteractive:    true,
		prepare: func(home, socket, self string) ([]string, error) {
			return []string{
				"ANTHROPIC_BASE_URL=" + proxy.baseURL(),
				"ANTHROPIC_API_KEY=" + proxy.capability,
				"CLAUDE_CODE_SIMPLE=1",
			}, nil
		},
	}
	return runPrepared("claude", folders, readOnly, binary, commandArgs, options)
}
