//go:build windows

package testutil

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// installWindowsFake makes the fake at path resolvable and launchable on
// windows by putting a native launcher beside it.
//
// exec.LookPath on windows only considers names carrying a PATHEXT extension
// (.COM;.EXE;.BAT;.CMD;...), so a bare `#!/bin/sh` file named `herdr` is
// invisible to it no matter what its mode bits say: production reports the
// binary as absent and the test measures a lookup failure rather than the
// contract it meant to exercise (#549 group 1). LookPath appends each PATHEXT
// entry to the whole name, so `herdr.exe` answers a lookup for `herdr` and the
// shell script beside it stays the single source of the fake's behaviour on
// both platforms.
//
// The launcher is a COPY of the running test binary, never a hard link to it.
// A hard link is a second name for the same file, so the launcher would share
// the running binary's image section, and windows denies unlink on a file with
// an active image section. Every fake would then be undeletable for the whole
// test process, and t.TempDir cleanup would fail with "Access is denied" --
// which is exactly what a hard-linked launcher did: it turned 245 PATH-lookup
// failures into 202 cleanup failures, a reclassification rather than a fix.
// That is structural, not a race: no retry or backoff reaches it. A copy is a
// distinct file, mapped only while the fake is actually executing, and
// runFakeLauncher's cmd.Run plus JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE guarantee
// the fake and its shell children have exited before cleanup runs.
func isExecutable(path string) bool {
	_, err := exec.LookPath(path)
	return err == nil
}

func installWindowsFake(path string) error {
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate test executable for fake %s: %w", filepath.Base(path), err)
	}
	script, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read fake script %s: %w", path, err)
	}
	shell, err := resolvePOSIXShell(bootPath)
	if err != nil {
		return err
	}
	launcher := path + ".exe"
	if err := os.Remove(launcher); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove existing fake launcher %s: %w", launcher, err)
	}
	if err := os.WriteFile(launcher+".fake.sh", script, 0o755); err != nil {
		return fmt.Errorf("write fake launcher sidecar %s: %w", launcher+".fake.sh", err)
	}
	if err := os.WriteFile(launcher+".fake.shell", []byte(shell), 0o600); err != nil {
		return fmt.Errorf("write fake shell metadata %s: %w", launcher+".fake.shell", err)
	}
	if err := copyFile(executable, launcher); err != nil {
		return fmt.Errorf("install fake launcher %s: %w", launcher, err)
	}
	return nil
}

func fakeExecutablePath(path string) string { return path + ".exe" }

func init() {
	run, err := isFakeLauncher()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if run {
		if err := runFakeLauncher(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
}

func isFakeLauncher() (bool, error) {
	executable, err := os.Executable()
	if err != nil {
		return false, err
	}
	if !strings.HasSuffix(filepath.Base(executable), ".exe") {
		return false, nil
	}
	_, err = os.Stat(executable + ".fake.sh")
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}

func runFakeLauncher() error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	sidecar := executable + ".fake.sh"
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return fmt.Errorf("fake launcher: create job object: %w", err)
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		return fmt.Errorf("fake launcher: configure job object: %w", err)
	}
	process, err := windows.GetCurrentProcess()
	if err != nil {
		return fmt.Errorf("fake launcher: get current process: %w", err)
	}
	if err := windows.AssignProcessToJobObject(job, process); err != nil {
		return fmt.Errorf("fake launcher: assign process to job object: %w", err)
	}
	shell, err := os.ReadFile(executable + ".fake.shell")
	if err != nil {
		return fmt.Errorf("fake launcher: read shell metadata: %w", err)
	}
	cmd := exec.Command(strings.TrimSpace(string(shell)), sidecar)
	cmd.Args = append([]string{cmd.Path, sidecar}, os.Args[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		return fmt.Errorf("fake launcher: run shell: %w", err)
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func posixShellPath() (string, error) { return resolvePOSIXShell(bootPath) }

func resolveBashShell(searchPath string) (string, []string, error) {
	candidates := make([]bashCandidate, 0, 2)
	if bash, dirs, ok := resolveGitBash(searchPath); ok {
		candidates = append(candidates, bashCandidate{shell: bash, preferredDirs: dirs})
	}
	if p := findOnPath(searchPath, "bash.exe"); p != "" {
		candidates = append(candidates, bashCandidate{shell: p})
	}
	return resolveBashCandidates(searchPath, candidates, "bash.exe", "cat.exe", "mkdir.exe")
}

// userHomeEnv is the variable os.UserHomeDir reads on this platform.
const userHomeEnv = "USERPROFILE"
