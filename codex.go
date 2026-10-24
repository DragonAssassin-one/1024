package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

func runCodex(args []string) error {
	return runCodexWithDeps(args, defaultConnectionStore, findCodexBinary, newModelProxyConfig, context.Background())
}

func runCodexWithDeps(args []string, storeForUser func() (connectionStore, error),
	nativeBinary func() (string, error), proxyConfig func(connectionStore) modelProxyConfig,
	ctx context.Context) error {
	return runCodexTask(args, storeForUser, nativeBinary, proxyConfig, ctx, nil)
}

func runCodexTask(args []string, storeForUser func() (connectionStore, error),
	nativeBinary func() (string, error), proxyConfig func(connectionStore) modelProxyConfig,
	ctx context.Context, output io.Writer) error {
	if runtime.GOOS != "darwin" {
		return errors.New("this prototype requires macOS Seatbelt")
	}
	flags := flag.NewFlagSet("codex", flag.ContinueOnError)
	model := flags.String("model", "", "OpenAI model ID")
	var requested foldersFlag
	var requestedReadOnly foldersFlag
	flags.Var(&requested, "folder", "authorized writable folder")
	flags.Var(&requestedReadOnly, "read-only", "authorized read-only folder")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(requested) == 0 || *model == "" || flags.NArg() != 1 || flags.Arg(0) == "" {
		return errors.New("usage: 1024 codex --folder PATH [--read-only PATH ...] --model MODEL -- PROMPT")
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
	binary, err := nativeBinary()
	if err != nil {
		return err
	}
	store, err := storeForUser()
	if err != nil {
		return err
	}
	proxy, err := startModelProxy(proxyConfig(store))
	if err != nil {
		return fmt.Errorf("model proxy: %w", err)
	}
	defer proxy.Close()
	options := runOptions{
		ctx:            ctx,
		output:         output,
		proxyPort:      proxy.port(),
		homeRoot:       filepath.Join(filepath.Dir(store.path), "sessions"),
		extraRead:      []string{"/etc/codex/requirements.toml", "/private/etc/codex/requirements.toml"},
		nonInteractive: true,
		prepare: func(home, socket, self string) ([]string, error) {
			codexHome := filepath.Join(home, ".codex")
			if err := os.Mkdir(codexHome, 0700); err != nil {
				return nil, err
			}
			config := codexConfig(*model, proxy, self)
			if err := os.WriteFile(filepath.Join(codexHome, "config.toml"), []byte(config), 0600); err != nil {
				return nil, err
			}
			return []string{"CODEX_HOME=" + codexHome}, nil
		},
	}
	commandArgs := []string{"exec", "--ephemeral", "--skip-git-repo-check",
		"--sandbox", "danger-full-access", "--cd", folders[0], flags.Arg(0)}
	return runPrepared("codex", folders, readOnly, binary, commandArgs, options)
}

func codexConfig(model string, proxy *modelProxy, self string) string {
	return fmt.Sprintf(`model = %s
model_provider = "keymoat"
approval_policy = "never"
sandbox_mode = "danger-full-access"

[model_providers.keymoat]
name = "KeyMoat host proxy"
base_url = %s
wire_api = "responses"
http_headers = {%s = %s}

[mcp_servers.keymoat]
command = %s
args = ["mcp"]
`, strconv.Quote(model), strconv.Quote(proxy.baseURL()),
		strconv.Quote(modelSessionHeader), strconv.Quote(proxy.capability), strconv.Quote(self))
}

func findCodexBinary() (string, error) {
	installed, err := exec.LookPath("codex")
	if err != nil {
		return "", err
	}
	installed, err = filepath.EvalSymlinks(installed)
	if err != nil {
		return "", err
	}
	if filepath.Ext(installed) != ".js" {
		return installed, nil
	}
	arch := map[string]string{"arm64": "aarch64", "amd64": "x86_64"}[runtime.GOARCH]
	if arch == "" {
		return "", errors.New("unsupported Codex architecture")
	}
	triple := arch + "-apple-darwin"
	packageRoot := filepath.Dir(filepath.Dir(installed))
	suffix := "arm64"
	if runtime.GOARCH == "amd64" {
		suffix = "x64"
	}
	native := filepath.Join(packageRoot, "node_modules", "@openai", "codex-darwin-"+suffix,
		"vendor", triple, "bin", "codex")
	info, err := os.Stat(native)
	if err != nil || info.IsDir() {
		return "", errors.New("Codex native binary not found; install the platform package")
	}
	return filepath.EvalSymlinks(native)
}
