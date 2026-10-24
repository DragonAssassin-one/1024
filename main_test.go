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
	profile := seatbeltProfile([]string{"/tmp/project"}, []string{"/tmp/reference"}, "/tmp/home", "/bin/sh", "/tmp/1024", "/tmp/home/broker.sock")
	for _, rule := range []string{
		"(deny default)",
		"(subpath \"/tmp/project\")",
		"(allow file-read* (subpath \"/tmp/reference\"))",
		"(subpath \"/tmp/home\")",
		"(literal \"/bin/sh\")",
		"(remote unix-socket (literal \"/tmp/home/broker.sock\"))",
	} {
		if !strings.Contains(profile, rule) {
			t.Fatalf("missing %s", rule)
		}
	}
	if strings.Contains(profile, "(allow network-outbound)") || strings.Contains(profile, "(allow default)") ||
		strings.Contains(profile, "(allow network-bind") ||
		strings.Contains(profile, "(allow file-write* (subpath \"/tmp/reference\"))") {
		t.Fatal("profile unexpectedly allows network or all operations")
	}
}

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "__probe" {
		os.Exit(probe(os.Args[2:]))
	}
	os.Exit(m.Run())
}

func TestRunDirect(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS only")
	}
	if err := run([]string{"run", "--agent", "codex", "--folder", t.TempDir(),
		"--read-only", t.TempDir(), "--", "/usr/bin/true"}); err != nil {
		t.Fatal(err)
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
	profile := seatbeltProfile([]string{project}, nil, home, binary, self, filepath.Join(home, "broker.sock"))
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
		"/bin/sh", "-c", `set -e; printf allowed > result; printf %s "$HOME" > home-path; test -r result; ! test -r "$1"; ! test -r link; ! /bin/cat link >/dev/null 2>&1; ! /usr/bin/env | /usr/bin/grep -q SECRET_MARKER`,
		"sh", outside)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v: %s", err, output)
	}
	content, err := os.ReadFile(filepath.Join(project, "result"))
	if err != nil || string(content) != "allowed" {
		t.Fatalf("authorized write failed: %q, %v", content, err)
	}
	homePath, err := os.ReadFile(filepath.Join(project, "home-path"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(string(homePath)); !os.IsNotExist(err) {
		t.Fatalf("temporary home remained after command: %v", err)
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
	exitCommand := exec.Command(binary, "run", "--agent", "dsh", "--folder", project, "--",
		"/bin/sh", "-c", "exit 23")
	output, err = exitCommand.CombinedOutput()
	if err == nil || exitCommand.ProcessState.ExitCode() != 23 {
		t.Fatalf("child exit code was lost: %v: %s", err, output)
	}
}

func TestInvalidAgentFailsBeforeLaunch(t *testing.T) {
	err := run([]string{"run", "--agent", "other", "--folder", ".", "--", "/bin/echo"})
	if err == nil || !strings.Contains(err.Error(), "agent must be") {
		t.Fatalf("expected agent validation error, got %v", err)
	}
}

func TestRunMultipleFolders(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS only")
	}
	binary := filepath.Join(t.TempDir(), "1024")
	build := exec.Command("go", "build", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v: %s", err, output)
	}
	first, second, outside := t.TempDir(), t.TempDir(), t.TempDir()
	secondCanonical, err := filepath.EvalSymlinks(second)
	if err != nil {
		t.Fatal(err)
	}
	outsideCanonical, err := filepath.EvalSymlinks(outside)
	if err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(first, alias); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(binary, "run", "--agent", "codex",
		"--folder", first, "--folder", second, "--folder", alias, "--",
		"/bin/sh", "-c", `set -e; printf first > result; printf second > "$1/result"; test -r "$1/result"; ! test -r "$2"`,
		"sh", secondCanonical, outsideCanonical)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("multi-folder run: %v: %s", err, output)
	}
	for path, expected := range map[string]string{first: "first", second: "second"} {
		content, err := os.ReadFile(filepath.Join(path, "result"))
		if err != nil || string(content) != expected {
			t.Fatalf("authorized folder %s: %q, %v", path, content, err)
		}
	}
	statusCommand := exec.Command(binary, "run", "--agent", "codex",
		"--folder", first, "--folder", second, "--folder", alias, "--", binary, "status")
	output, err := statusCommand.CombinedOutput()
	if err != nil || strings.Count(string(output), "Folder      ") != 2 {
		t.Fatalf("folder status was not deduplicated: %v: %s", err, output)
	}
}

func TestProtectedSecondFolderFails(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS only")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	err = run([]string{"run", "--agent", "codex", "--folder", t.TempDir(),
		"--folder", home, "--", "/bin/echo"})
	if err == nil || !strings.Contains(err.Error(), "protected host state") {
		t.Fatalf("protected second folder was accepted: %v", err)
	}
}

func TestReadOnlyFolder(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS only")
	}
	binary := filepath.Join(t.TempDir(), "1024")
	build := exec.Command("go", "build", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v: %s", err, output)
	}
	writable, readOnly := t.TempDir(), t.TempDir()
	readOnly, err := filepath.EvalSymlinks(readOnly)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(readOnly, "source"), []byte("reference"), 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(binary, "run", "--agent", "codex", "--folder", writable,
		"--read-only", readOnly, "--", "/bin/sh", "-c",
		`set -e; test "$(/bin/cat "$1/source")" = reference; ! /usr/bin/touch "$1/new" 2>/dev/null; printf allowed > result`,
		"sh", readOnly)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("read-only run: %v: %s", err, output)
	}
	if _, err := os.Stat(filepath.Join(readOnly, "new")); !os.IsNotExist(err) {
		t.Fatalf("read-only folder was modified: %v", err)
	}
	if content, err := os.ReadFile(filepath.Join(writable, "result")); err != nil || string(content) != "allowed" {
		t.Fatalf("writable folder failed: %q, %v", content, err)
	}
	status := exec.Command(binary, "run", "--agent", "codex", "--folder", writable,
		"--read-only", readOnly, "--", binary, "status")
	if output, err := status.CombinedOutput(); err != nil || !strings.Contains(string(output), "Read only   "+readOnly) {
		t.Fatalf("status omitted read-only folder: %v: %s", err, output)
	}
}

func TestFolderOverlapFails(t *testing.T) {
	writable := t.TempDir()
	child := filepath.Join(writable, "child")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{{writable, writable}, {writable, child}, {child, writable}} {
		if err := validateFolderOverlap([]string{pair[0]}, []string{pair[1]}); err == nil {
			t.Fatalf("accepted overlapping folders: %v", pair)
		}
	}
	if err := validateFolderOverlap([]string{writable}, []string{t.TempDir()}); err != nil {
		t.Fatalf("rejected disjoint folders: %v", err)
	}
	if err := validateFolderOverlap([]string{writable}, []string{"/dev"}); err == nil {
		t.Fatal("accepted read-only folder containing writable device")
	}
	err := run([]string{"run", "--agent", "codex", "--folder", writable,
		"--read-only", child, "--", "/bin/echo"})
	if err == nil || !strings.Contains(err.Error(), "overlaps writable") {
		t.Fatalf("overlap was accepted before launch: %v", err)
	}
}
