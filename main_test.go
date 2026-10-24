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
	profile := seatbeltProfile("/tmp/project", "/tmp/home", "/bin/sh", "/tmp/1024")
	for _, rule := range []string{
		"(deny default)",
		"(subpath \"/tmp/project\")",
		"(subpath \"/tmp/home\")",
		"(literal \"/bin/sh\")",
	} {
		if !strings.Contains(profile, rule) {
			t.Fatalf("missing %s", rule)
		}
	}
	if strings.Contains(profile, "(allow network") || strings.Contains(profile, "(allow default)") {
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
	profile := seatbeltProfile(project, home, binary, self)
	command := exec.Command("/usr/bin/sandbox-exec", "-p", profile, binary, "hello")
	command.Env = sandboxEnv(home)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("sandbox-exec: %v: %s\nprofile:\n%s", err, output, profile)
	}
	if string(output) != "hello\n" {
		t.Fatalf("unexpected output: %q", output)
	}
}
