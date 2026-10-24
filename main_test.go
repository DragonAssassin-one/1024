package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestProfileScope(t *testing.T) {
	profile := seatbeltProfile("/tmp/project", "/tmp/home", "/bin/sh", "/tmp/1024", "/tmp/home/broker.sock")
	for _, rule := range []string{
		"(deny default)",
		"(subpath \"/tmp/project\")",
		"(subpath \"/tmp/home\")",
		"(literal \"/bin/sh\")",
		"(remote unix-socket (literal \"/tmp/home/broker.sock\"))",
	} {
		if !strings.Contains(profile, rule) {
			t.Fatalf("missing %s", rule)
		}
	}
	if strings.Contains(profile, "(allow network-outbound)") || strings.Contains(profile, "(allow default)") ||
		strings.Contains(profile, "(allow network-bind") {
		t.Fatal("profile unexpectedly allows network or all operations")
	}
}

func TestSandboxExecSmoke(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS only")
	}
	project := t.TempDir()
	home := t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := filepath.EvalSymlinks("/bin/echo")
	if err != nil {
		t.Fatal(err)
	}
	profile := seatbeltProfile(project, home, binary, self, filepath.Join(home, "broker.sock"))
	command := exec.Command("/usr/bin/sandbox-exec", "-p", profile, binary, "hello")
	command.Env = sandboxEnv(home, filepath.Join(home, "broker.sock"))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("sandbox-exec: %v: %s\nprofile:\n%s", err, output, profile)
	}
	if string(output) != "hello\n" {
		t.Fatalf("unexpected output: %q", output)
	}
}

func TestRunIsolation(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS only")
	}
	binary := filepath.Join(t.TempDir(), "1024")
	build := exec.Command("go", "build", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v: %s", err, output)
	}
	project := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(project, "link")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SECRET_MARKER", "must-not-be-inherited")
	command := exec.Command(binary, "run", "--agent", "codex", "--folder", project, "--",
		"/bin/sh", "-c", `set -e; printf allowed > result; test -r result; ! test -r "$1"; ! test -r link; ! /bin/cat link >/dev/null 2>&1; ! /usr/bin/env | /usr/bin/grep -q SECRET_MARKER`,
		"sh", outside)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v: %s", err, output)
	}
	content, err := os.ReadFile(filepath.Join(project, "result"))
	if err != nil || string(content) != "allowed" {
		t.Fatalf("authorized write failed: %q, %v", content, err)
	}
	content, err = os.ReadFile(outside)
	if err != nil || string(content) != "private" {
		t.Fatalf("outside file changed: %q, %v", content, err)
	}
	statusCommand := exec.Command(binary, "run", "--agent", "claude", "--folder", project, "--", binary, "status")
	output, err = statusCommand.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "Agent       claude") ||
		!strings.Contains(string(output), "Connections 0") {
		t.Fatalf("sandbox status: %v: %s", err, output)
	}
}

func TestInvalidAgentFailsBeforeLaunch(t *testing.T) {
	err := run([]string{"run", "--agent", "other", "--folder", ".", "--", "/bin/echo"})
	if err == nil || !strings.Contains(err.Error(), "agent must be") {
		t.Fatalf("expected agent validation error, got %v", err)
	}
}
