package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func runSandbox(args []string) error {
	if runtime.GOOS != "darwin" {
		return errors.New("this prototype requires macOS Seatbelt")
	}
	if len(args) == 0 || args[0] != "run" {
		return errors.New("usage: 1024 run --agent claude|codex|dsh --folder PATH [--folder PATH ...] [--read-only PATH ...] -- COMMAND [ARGS...]")
	}
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	agent := flags.String("agent", "", "agent identity")
	var requested foldersFlag
	var requestedReadOnly foldersFlag
	flags.Var(&requested, "folder", "authorized project folder (repeatable)")
	flags.Var(&requestedReadOnly, "read-only", "read-only folder (repeatable)")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if *agent != "claude" && *agent != "codex" && *agent != "dsh" {
		return errors.New("agent must be claude, codex, or dsh")
	}
	if len(requested) == 0 || flags.NArg() == 0 {
		return errors.New("folder and command are required")
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
	return runPrepared(*agent, folders, readOnly, flags.Arg(0), flags.Args()[1:], runOptions{})
}

type runOptions struct {
	proxyPort         int
	homeRoot          string
	ctx               context.Context
	extraRead         []string
	extraReadMetadata []string
	store             *connectionStore
	nonInteractive    bool
	output            io.Writer
	prepare           func(home, socket, self string) ([]string, error)
}

func runPrepared(agent string, folders, readOnly []string, executable string, args []string, options runOptions) error {
	project := folders[0]
	binary, err := exec.LookPath(executable)
	if err != nil {
		return err
	}
	binary, err = filepath.EvalSymlinks(binary)
	if err != nil {
		return err
	}
	binary, err = filepath.Abs(binary)
	if err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	self, err = filepath.EvalSymlinks(self)
	if err != nil {
		return err
	}
	root := options.homeRoot
	if root != "" {
		if err := os.MkdirAll(root, 0700); err != nil {
			return err
		}
	}
	home, err := os.MkdirTemp(root, "1024-"+agent+"-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(home)
	home, err = filepath.EvalSymlinks(home)
	if err != nil {
		return err
	}
	if err := validateFolderOverlap([]string{home}, readOnly); err != nil {
		return err
	}
	outside, err := os.CreateTemp("", "1024-outside-")
	if err != nil {
		return err
	}
	outsidePath := outside.Name()
	outside.Close()
	defer os.Remove(outsidePath)

	store := options.store
	if store == nil {
		defaultStore, err := defaultConnectionStore()
		if err != nil {
			return err
		}
		store = &defaultStore
	}
	broker, brokerPath, err := startBroker(home, agent, folders, readOnly, newGitHubService(*store))
	if err != nil {
		return fmt.Errorf("broker: %w", err)
	}
	defer broker.Close()
	profile := seatbeltProfile(folders, readOnly, home, binary, self, brokerPath)
	for _, path := range options.extraRead {
		scope := "literal"
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			scope = "subpath"
		}
		profile += "\n(allow file-read* (" + scope + " " + strconv.Quote(path) + "))"
		for parent := filepath.Dir(path); parent != "/" && parent != "."; parent = filepath.Dir(parent) {
			profile += "\n(allow file-read-metadata (literal " + strconv.Quote(parent) + "))"
		}
	}
	for _, path := range options.extraReadMetadata {
		profile += "\n(allow file-read-metadata (literal " + strconv.Quote(path) + "))"
	}
	if options.proxyPort != 0 {
		profile += fmt.Sprintf("\n(allow network-outbound (remote ip \"localhost:%d\"))", options.proxyPort)
	}
	coverageDir := permittedCoverageDir(folders)
	if err := verify(profile, folders, readOnly, home, outsidePath, self, brokerPath, agent, coverageDir, options.proxyPort); err != nil {
		return fmt.Errorf("sandbox verification failed (command not started): %w", err)
	}
	extraEnv := []string{}
	if options.prepare != nil {
		extraEnv, err = options.prepare(home, brokerPath, self)
		if err != nil {
			return err
		}
	}
	ctx := options.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	command := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", append([]string{"-p", profile, binary}, args...)...)
	if options.ctx != nil {
		command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		command.Cancel = func() error {
			if command.Process == nil {
				return os.ErrProcessDone
			}
			return syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		}
		command.WaitDelay = 2 * time.Second
	}
	command.Env = sandboxEnv(home, brokerPath, coverageDir)
	command.Env = append(command.Env, extraEnv...)
	command.Dir = project
	if !options.nonInteractive {
		command.Stdin = os.Stdin
	}
	output := options.output
	if output == nil {
		output = os.Stdout
	}
	command.Stdout, command.Stderr = output, output
	if err := command.Run(); err != nil {
		return err
	}
	return nil
}

func canonicalFolders(requested foldersFlag) ([]string, error) {
	folders := make([]string, 0, len(requested))
	seen := make(map[string]bool)
	for _, requestedFolder := range requested {
		project, err := canonicalDir(requestedFolder)
		if err != nil {
			return nil, fmt.Errorf("folder %q: %w", requestedFolder, err)
		}
		if err := validateProject(project); err != nil {
			return nil, err
		}
		if !seen[project] {
			folders = append(folders, project)
			seen[project] = true
		}
	}
	return folders, nil
}

func validateFolderOverlap(writable, readOnly []string) error {
	for _, readPath := range readOnly {
		for _, device := range []string{"/dev/null", "/dev/tty"} {
			if containsPath(readPath, device) {
				return fmt.Errorf("read-only folder includes writable system device: %s", readPath)
			}
		}
		for _, writePath := range writable {
			if containsPath(writePath, readPath) || containsPath(readPath, writePath) {
				return fmt.Errorf("read-only folder overlaps writable folder: %s and %s", readPath, writePath)
			}
		}
	}
	return nil
}

type foldersFlag []string

func (f *foldersFlag) String() string {
	return strings.Join(*f, ", ")
}

func (f *foldersFlag) Set(value string) error {
	if value == "" {
		return errors.New("folder cannot be empty")
	}
	*f = append(*f, value)
	return nil
}

func canonicalDir(path string) (string, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("not a directory")
	}
	return path, nil
}

func seatbeltProfile(folders, readOnly []string, home, binary, self, socket string) string {
	quote := strconv.Quote
	rules := []string{
		"(version 1)",
		"(deny default)",
		"(allow process-exec)",
		"(allow process-fork)",
		"(allow file-read* (literal \"/\") (subpath \"/System\") (subpath \"/usr/lib\") (subpath \"/usr/share\") (subpath \"/bin\") (subpath \"/usr/bin\"))",
		"(allow file-read* (literal \"/dev/null\") (literal \"/dev/urandom\") (literal \"/dev/random\") (literal \"/dev/tty\"))",
		"(allow file-read* (subpath " + quote(home) + "))",
		"(allow file-read* (literal " + quote(binary) + ") (literal " + quote(self) + "))",
		"(allow file-write* (subpath " + quote(home) + "))",
		"(allow file-write* (literal \"/dev/null\") (literal \"/dev/tty\"))",
		"(allow sysctl-read)",
		"(allow system-socket (socket-domain AF_UNIX))",
		"(allow network-outbound (remote unix-socket (literal " + quote(socket) + ")))",
	}
	for _, folder := range folders {
		rules = append(rules,
			"(allow file-read* (subpath "+quote(folder)+"))",
			"(allow file-write* (subpath "+quote(folder)+"))")
	}
	for _, folder := range readOnly {
		rules = append(rules, "(allow file-read* (subpath "+quote(folder)+"))")
	}
	seen := map[string]bool{}
	for _, path := range append(append(append([]string(nil), folders...), readOnly...), home, binary, self) {
		for parent := filepath.Dir(path); parent != "." && parent != "/"; parent = filepath.Dir(parent) {
			if !seen[parent] {
				rules = append(rules, "(allow file-read-metadata (literal "+quote(parent)+"))")
				seen[parent] = true
			}
		}
	}
	return strings.Join(rules, "\n")
}

func permittedCoverageDir(folders []string) string {
	path := os.Getenv("GOCOVERDIR")
	if path == "" {
		return ""
	}
	path, err := canonicalDir(path)
	if err != nil {
		return ""
	}
	for _, folder := range folders {
		if containsPath(folder, path) {
			return path
		}
	}
	return ""
}

func sandboxEnv(home, socket, coverageDir string) []string {
	env := []string{
		"HOME=" + home,
		"TMPDIR=" + home,
		"PATH=/usr/bin:/bin",
		"LANG=C",
		brokerSocketEnv + "=" + socket,
	}
	if coverageDir != "" {
		env = append(env, "GOCOVERDIR="+coverageDir)
	}
	return env
}

type folderPermissions struct {
	Writable []string `json:"writable"`
	ReadOnly []string `json:"read_only"`
}

func verify(profile string, folders, readOnly []string, home, outside, self, socket, agent, coverageDir string, proxyPort ...int) error {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer listener.Close()
	other, err := net.Listen("unix", outside+".sock")
	if err != nil {
		return err
	}
	defer other.Close()
	permissions, err := json.Marshal(folderPermissions{folders, readOnly})
	if err != nil {
		return err
	}
	probeArgs := []string{"-p", profile, self, "__probe", string(permissions),
		home, outside, listener.Addr().String(), socket, other.Addr().String(), agent}
	if len(proxyPort) > 0 && proxyPort[0] != 0 {
		probeArgs = append(probeArgs, strconv.Itoa(proxyPort[0]))
	}
	command := exec.Command("/usr/bin/sandbox-exec", probeArgs...)
	command.Env = sandboxEnv(home, socket, coverageDir)
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func probe(args []string) int {
	if len(args) != 7 && len(args) != 8 {
		return 2
	}
	var permissions folderPermissions
	if err := json.Unmarshal([]byte(args[0]), &permissions); err != nil || len(permissions.Writable) == 0 {
		return probeFailure("invalid folder permissions", err)
	}
	home, outside, tcp, socket, otherSocket, agent := args[1], args[2], args[3], args[4], args[5], args[6]
	for _, folder := range append(append([]string(nil), permissions.Writable...), permissions.ReadOnly...) {
		if _, err := os.ReadDir(folder); err != nil {
			return probeFailure("allowed folder is not readable", err)
		}
	}
	for _, folder := range permissions.ReadOnly {
		file, err := os.CreateTemp(folder, ".1024-write-probe-")
		if err == nil {
			file.Close()
			os.Remove(file.Name())
			return probeFailure("read-only folder was writable", nil)
		}
		if !errors.Is(err, os.ErrPermission) {
			return probeFailure("read-only denial was not confirmed", err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, "probe"), []byte("ok"), 0600); err != nil {
		return probeFailure("private home is not writable", err)
	}
	if _, err := os.ReadFile(outside); err == nil || !errors.Is(err, os.ErrPermission) {
		return probeFailure("outside file was not denied", err)
	}
	if err := os.WriteFile(outside, []byte("bad"), 0600); err == nil || !errors.Is(err, os.ErrPermission) {
		return probeFailure("outside write was not denied", err)
	}
	status, err := readStatus(socket)
	if err != nil || status.Agent != agent || status.Folder != permissions.Writable[0] ||
		!slices.Equal(status.Folders, permissions.Writable) ||
		!slices.Equal(status.ReadOnlyFolders, permissions.ReadOnly) {
		return probeFailure("host status channel is unavailable or incorrect", err)
	}
	if err := deniedDial("unix", otherSocket); err != nil {
		return probeFailure("outside unix socket was not denied", err)
	}
	if err := deniedDial("tcp", tcp); err != nil {
		return probeFailure("network denial was not confirmed", err)
	}
	if len(args) == 8 {
		port, err := strconv.Atoi(args[7])
		if err != nil {
			return probeFailure("invalid proxy port", err)
		}
		conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 500*time.Millisecond)
		if err != nil {
			return probeFailure("authorized model proxy unavailable", err)
		}
		conn.Close()
	}
	return 0
}

func deniedDial(network, address string) error {
	conn, err := net.DialTimeout(network, address, 500*time.Millisecond)
	if conn != nil {
		conn.Close()
		return errors.New("connection was allowed")
	}
	if !errors.Is(err, syscall.EPERM) && !errors.Is(err, syscall.EACCES) {
		return err
	}
	return nil
}

func probeFailure(message string, err error) int {
	fmt.Fprintln(os.Stderr, message, err)
	return 1
}
