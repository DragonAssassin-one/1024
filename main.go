package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
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

func main() {
	if len(os.Args) > 1 && os.Args[1] == "__probe" {
		os.Exit(probe(os.Args[2:]))
	}
	if len(os.Args) > 1 && os.Args[1] == "status" {
		if err := printStatus(); err != nil {
			fmt.Fprintln(os.Stderr, "1024:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "mcp" {
		if err := runMCP(context.Background()); err != nil {
			fmt.Fprintln(os.Stderr, "1024:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && (os.Args[1] == "credential" || os.Args[1] == "connect" || os.Args[1] == "disconnect") {
		if err := control(os.Args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, "1024:", err)
			os.Exit(1)
		}
		return
	}
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "1024:", err)
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() > 0 {
			os.Exit(exit.ExitCode())
		}
		os.Exit(1)
	}
}

func run(args []string) error {
	if runtime.GOOS != "darwin" {
		return errors.New("this prototype requires macOS Seatbelt")
	}
	if len(args) == 0 || args[0] != "run" {
		return errors.New("usage: 1024 run --agent claude|codex|dsh --folder PATH [--folder PATH ...] -- COMMAND [ARGS...]")
	}
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	agent := flags.String("agent", "", "agent identity")
	var requested foldersFlag
	flags.Var(&requested, "folder", "authorized project folder (repeatable)")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if *agent != "claude" && *agent != "codex" && *agent != "dsh" {
		return errors.New("agent must be claude, codex, or dsh")
	}
	if len(requested) == 0 || flags.NArg() == 0 {
		return errors.New("folder and command are required")
	}
	folders := make([]string, 0, len(requested))
	seen := make(map[string]bool)
	for _, requestedFolder := range requested {
		project, err := canonicalDir(requestedFolder)
		if err != nil {
			return fmt.Errorf("folder %q: %w", requestedFolder, err)
		}
		if err := validateProject(project); err != nil {
			return err
		}
		if !seen[project] {
			folders = append(folders, project)
			seen[project] = true
		}
	}
	project := folders[0]
	binary, err := exec.LookPath(flags.Arg(0))
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
	home, err := os.MkdirTemp("", "1024-"+*agent+"-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(home)
	home, err = filepath.EvalSymlinks(home)
	if err != nil {
		return err
	}
	outside, err := os.CreateTemp("", "1024-outside-")
	if err != nil {
		return err
	}
	outsidePath := outside.Name()
	outside.Close()
	defer os.Remove(outsidePath)

	store, err := defaultConnectionStore()
	if err != nil {
		return err
	}
	broker, brokerPath, err := startBroker(home, *agent, folders, newGitHubService(store))
	if err != nil {
		return fmt.Errorf("broker: %w", err)
	}
	defer broker.Close()
	profile := seatbeltProfile(folders, home, binary, self, brokerPath)
	if err := verify(profile, folders, home, outsidePath, self, brokerPath, *agent); err != nil {
		return fmt.Errorf("sandbox verification failed (command not started): %w", err)
	}
	command := exec.Command("/usr/bin/sandbox-exec", append([]string{"-p", profile, binary}, flags.Args()[1:]...)...)
	command.Env = sandboxEnv(home, brokerPath)
	command.Dir = project
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := command.Run(); err != nil {
		return err
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

func seatbeltProfile(folders []string, home, binary, self, socket string) string {
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
	return strings.Join(rules, "\n")
}

func sandboxEnv(home, socket string) []string {
	return []string{
		"HOME=" + home,
		"TMPDIR=" + home,
		"PATH=/usr/bin:/bin",
		"LANG=C",
		brokerSocketEnv + "=" + socket,
	}
}

func verify(profile string, folders []string, home, outside, self, socket, agent string) error {
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
	probeArgs := append([]string{"-p", profile, self, "__probe"}, folders...)
	probeArgs = append(probeArgs, home, outside, listener.Addr().String(), socket, other.Addr().String(), agent)
	command := exec.Command("/usr/bin/sandbox-exec", probeArgs...)
	command.Env = sandboxEnv(home, socket)
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func probe(args []string) int {
	if len(args) < 7 {
		return 2
	}
	folders := args[:len(args)-6]
	home, outside, tcp, socket, otherSocket, agent := args[len(args)-6], args[len(args)-5],
		args[len(args)-4], args[len(args)-3], args[len(args)-2], args[len(args)-1]
	for _, folder := range folders {
		if _, err := os.ReadDir(folder); err != nil {
			return probeFailure("allowed folder is not readable", err)
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
	if err != nil || status.Agent != agent || status.Folder != folders[0] ||
		!slices.Equal(status.Folders, folders) {
		return probeFailure("host status channel is unavailable or incorrect", err)
	}
	if err := deniedDial("unix", otherSocket); err != nil {
		return probeFailure("outside unix socket was not denied", err)
	}
	if err := deniedDial("tcp", tcp); err != nil {
		return probeFailure("network denial was not confirmed", err)
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
