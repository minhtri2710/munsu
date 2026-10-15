package fleet

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/testutil"
)

// fakeSeatMunsu records each run's cwd, args and seat variable to rec, prints
// to stdout, exits 3 for "task fail" and waits for a signal for "task sleep".
func fakeSeatMunsu(t *testing.T, rec string) string {
	t.Helper()
	return testutil.WriteFakeExecutable(t, filepath.Join(t.TempDir(), "munsu"), "#!/bin/sh\n"+
		"printf '%s|%s|%s\\n' \"$(pwd -P)\" \"$*\" \"${"+CaptainSeatEnv+"-unset}\" >> '"+rec+"'\n"+
		"case \"$2\" in\n"+
		"fail) exit 3;;\n"+
		"sleep) trap 'echo term >> \""+rec+"\"; exit 143' TERM; sleep 30 & wait; exit 0;;\n"+
		"esac\n"+
		"echo out-from-seat\n")
}

// seatServer serves seat requests on a fresh socket the way the seat does,
// running fake for each one in captainHome.
func seatServer(t *testing.T, fake, captainHome string) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "seat-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s.sock")
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	env := withoutEnv(os.Environ(), CaptainSeatEnv)
	go func() {
		for {
			c, err := l.AcceptUnix()
			if err != nil {
				return
			}
			go serveCaptainSeatRequest(c, fake, captainHome, env)
		}
	}()
	return socket
}

// sendSeatRequest writes a request as ForwardCaptainSeat does, with fds as
// the descriptors and size as the length it declares (the body's own when 0).
func sendSeatRequest(t *testing.T, socket string, args []string, fds []int, size uint32) *net.UnixConn {
	t.Helper()
	c, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(captainSeatRequest{Args: args})
	if size == 0 {
		size = uint32(len(body))
	}
	var head [4]byte
	binary.BigEndian.PutUint32(head[:], size)
	var oob []byte
	if fds != nil {
		oob = syscall.UnixRights(fds...)
	}
	if _, _, err := c.WriteMsgUnix(head[:], oob, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Write(body); err != nil {
		t.Fatal(err)
	}
	return c
}

func readSeatReply(t *testing.T, c *net.UnixConn) captainSeatReply {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(10 * time.Second))
	var reply captainSeatReply
	if err := json.NewDecoder(c).Decode(&reply); err != nil {
		t.Fatalf("reading the reply: %v", err)
	}
	return reply
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatal("timed out")
}

// The seat runs a seat command in its captain home with its own environment,
// on the requester's stdio, and returns its exit code; it refuses a malformed
// request before running anything, and stops a command whose requester hangs
// up.
func TestCaptainSeatRequests(t *testing.T) {
	captainHome, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rec := filepath.Join(t.TempDir(), "rec")
	socket := seatServer(t, fakeSeatMunsu(t, rec), captainHome)
	stdio := func(t *testing.T) (*os.File, []int) {
		out, err := os.Create(filepath.Join(t.TempDir(), "out"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { out.Close() })
		return out, []int{int(os.Stdin.Fd()), int(out.Fd()), int(out.Fd())}
	}
	records := func() string { b, _ := os.ReadFile(rec); return string(b) }

	t.Run("a seat command runs in the captain home on the requester's stdio", func(t *testing.T) {
		os.Remove(rec)
		out, fds := stdio(t)
		reply := readSeatReply(t, sendSeatRequest(t, socket, []string{"task", "list"}, fds, 0))
		got, _ := os.ReadFile(out.Name())
		if reply != (captainSeatReply{}) || records() != captainHome+"|task list|unset\n" || string(got) != "out-from-seat\n" {
			t.Fatalf("reply %+v, record %q, stdout %q", reply, records(), got)
		}
	})
	t.Run("its exit code comes back", func(t *testing.T) {
		_, fds := stdio(t)
		if reply := readSeatReply(t, sendSeatRequest(t, socket, []string{"task", "fail"}, fds, 0)); reply != (captainSeatReply{Exit: 3}) {
			t.Fatalf("reply %+v, want exit 3", reply)
		}
	})
	for _, tt := range []struct {
		name string
		args []string
		fds  func([]int) []int
		size uint32
		want string
	}{
		{"a command that is no seat command", []string{"config", "set", "x", "y"}, nil, 0, "runs only inside the captain's fence"},
		{"a request without its stdio", []string{"task", "list"}, func([]int) []int { return nil }, 0, "carries 0 descriptors"},
		{"a request with two descriptors", []string{"task", "list"}, func(f []int) []int { return f[:2] }, 0, "carries 2 descriptors"},
		{"an oversized request", []string{"task", "list"}, nil, captainSeatMaxRequest + 1, "exceeds"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			os.Remove(rec)
			_, fds := stdio(t)
			if tt.fds != nil {
				fds = tt.fds(fds)
			}
			reply := readSeatReply(t, sendSeatRequest(t, socket, tt.args, fds, tt.size))
			if reply.Exit != 1 || !strings.Contains(reply.Error, tt.want) || records() != "" {
				t.Fatalf("reply %+v, record %q; want %q refused before any run", reply, records(), tt.want)
			}
		})
	}
	t.Run("a requester that hangs up stops its command", func(t *testing.T) {
		os.Remove(rec)
		_, fds := stdio(t)
		c := sendSeatRequest(t, socket, []string{"task", "sleep"}, fds, 0)
		waitFor(t, func() bool { return strings.Contains(records(), "task sleep") })
		c.Close()
		waitFor(t, func() bool { return strings.Contains(records(), "term\n") })
	})
	t.Run("ForwardCaptainSeat returns the exit code and the refusal", func(t *testing.T) {
		wd, _ := os.Getwd()
		t.Cleanup(func() { os.Chdir(wd) })
		if code, err := ForwardCaptainSeat(socket, []string{"task", "fail"}); code != 3 || err != nil {
			t.Fatalf("ForwardCaptainSeat = %d, %v; want 3", code, err)
		}
		if code, err := ForwardCaptainSeat(socket, []string{"task", "list", "--home", "/g"}); code != 1 || err == nil || !strings.Contains(err.Error(), "--home is refused") {
			t.Fatalf("ForwardCaptainSeat = %d, %v; want the --home refusal", code, err)
		}
	})
}

// captainSeatClient names, for TestCaptainSeatClientChild, the file its
// requests' exit codes go to.
const captainSeatClient = "MUNSU_TEST_SEAT_CLIENT"

func TestCaptainSeatClientChild(t *testing.T) {
	out := os.Getenv(captainSeatClient)
	if out == "" {
		t.Skip("runs only as the captain of TestServeCaptainSeatRunsTheCaptain")
	}
	socket := os.Getenv(CaptainSeatEnv)
	listCode, listErr := ForwardCaptainSeat(socket, []string{"task", "list"})
	failCode, failErr := ForwardCaptainSeat(socket, []string{"task", "fail"})
	os.WriteFile(out, []byte(strings.Join([]string{socket, strconv.Itoa(listCode), fmt.Sprint(listErr), strconv.Itoa(failCode), fmt.Sprint(failErr)}, "|")), 0o600)
	os.Exit(failCode)
}

// The seat runs the captain with its socket named in the environment and
// returns the captain's exit code; the captain's requests run with the seat's
// environment, which does not name the socket, and the socket is gone after.
// A stale socket file is replaced.
func TestServeCaptainSeatRunsTheCaptain(t *testing.T) {
	captainHome, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(captainHome, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	socket := captainSeatSocket(captainHome)
	if err := os.WriteFile(socket, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	rec, out := filepath.Join(t.TempDir(), "rec"), filepath.Join(t.TempDir(), "out")
	fake := fakeSeatMunsu(t, rec)
	orig := captainSeatMunsu
	t.Cleanup(func() { captainSeatMunsu = orig })
	captainSeatMunsu = func() (string, error) { return fake, nil }
	t.Setenv(captainSeatClient, out)
	code, err := ServeCaptainSeat(captainHome, []string{os.Args[0], "-test.run=^TestCaptainSeatClientChild$"})
	if err != nil || code != 3 {
		t.Fatalf("ServeCaptainSeat = %d, %v; want the captain's 3", code, err)
	}
	if got, _ := os.ReadFile(out); string(got) != socket+"|0|<nil>|3|<nil>" {
		t.Fatalf("captain saw %q", got)
	}
	if got, _ := os.ReadFile(rec); string(got) != captainHome+"|task list|unset\n"+captainHome+"|task fail|unset\n" {
		t.Fatalf("seat ran %q", got)
	}
	if _, err := os.Lstat(socket); !os.IsNotExist(err) {
		t.Fatalf("socket left behind: %v", err)
	}
	if _, err := ServeCaptainSeat(captainHome, nil); err == nil || !strings.Contains(err.Error(), "no captain command") {
		t.Fatalf("err = %v, want the no-command refusal", err)
	}
}

// The captain's open descriptors are its stdio alone: a descriptor the seat's
// parent holds without close-on-exec does not reach it, nor does the seat's
// own socket.
func TestServeCaptainSeatCaptainHoldsOnlyStdio(t *testing.T) {
	captainHome, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(captainHome, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	held, err := os.Create(filepath.Join(t.TempDir(), "held"))
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	leak, err := syscall.Dup(int(held.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { syscall.Close(leak) })
	out, err := os.Create(filepath.Join(t.TempDir(), "fds"))
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	orig := os.Stdout
	os.Stdout = out
	code, err := ServeCaptainSeat(captainHome, []string{"/bin/sh", "-c", openFDsScript})
	os.Stdout = orig
	if err != nil || code != 0 {
		t.Fatalf("ServeCaptainSeat = %d, %v", code, err)
	}
	got, _ := os.ReadFile(out.Name())
	if string(got) != "" {
		t.Fatalf("captain holds descriptors %q above stdio; the parent's leaked one is %d", got, leak)
	}
}
