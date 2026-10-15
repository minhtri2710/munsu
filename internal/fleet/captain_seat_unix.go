package fleet

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// captainSeatMaxRequest bounds one request's argv encoding.
const captainSeatMaxRequest = 1 << 20

// captainSeatMunsu is the munsu binary the seat runs requests with; tests
// replace it.
var captainSeatMunsu = os.Executable

type captainSeatRequest struct {
	Args []string `json:"args"`
}

type captainSeatReply struct {
	Exit  int    `json:"exit"`
	Error string `json:"error,omitempty"`
}

// ServeCaptainSeat runs argv, the fenced captain, as its child, and until the
// child exits runs each request it receives on the captain's seat socket: a
// seat command's args and the requester's stdin, stdout and stderr. The
// command is this munsu binary run outside the fence, in captainHome, with the
// seat's own environment; nothing of the requester's environment, cwd or home
// is used. It returns the captain's exit code.
func ServeCaptainSeat(captainHome string, argv []string) (int, error) {
	if len(argv) == 0 {
		return 0, fmt.Errorf("captain seat: no captain command")
	}
	self, err := captainSeatMunsu()
	if err != nil {
		return 0, fmt.Errorf("captain seat: resolving munsu: %w", err)
	}
	socket := captainSeatSocket(captainHome)
	l, err := listenCaptainSeat(socket)
	if err != nil {
		return 0, err
	}
	defer os.Remove(socket)
	defer l.Close()
	env := withoutEnv(os.Environ(), CaptainSeatEnv)
	go func() {
		for {
			c, err := l.AcceptUnix()
			if err != nil {
				return
			}
			go serveCaptainSeatRequest(c, self, captainHome, env)
		}
	}()

	captain := exec.Command(argv[0], argv[1:]...)
	captain.Stdin, captain.Stdout, captain.Stderr = os.Stdin, os.Stdout, os.Stderr
	captain.Env = append(env, CaptainSeatEnv+"="+socket)
	// The terminal signals the captain itself; the seat outlives a ^C and
	// passes a hangup or termination on.
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGHUP, syscall.SIGTERM)
	defer signal.Stop(signals)
	if err := markInheritedCloseOnExec(); err != nil {
		return 0, fmt.Errorf("captain seat: %w", err)
	}
	if err := captain.Start(); err != nil {
		return 0, fmt.Errorf("captain seat: starting the captain: %w", err)
	}
	go func() {
		for s := range signals {
			if s == syscall.SIGHUP || s == syscall.SIGTERM {
				_ = captain.Process.Signal(s)
			}
		}
	}()
	return exitCode(captain.Wait())
}

// markInheritedCloseOnExec sets close-on-exec on every descriptor above stdio
// that this process inherited without it. os/exec passes only stdio and the
// descriptors it is given, so an inherited descriptor without the flag would
// reach the fenced captain.
func markInheritedCloseOnExec() error {
	dir, err := os.Open("/dev/fd")
	if err != nil {
		return err
	}
	names, err := dir.Readdirnames(-1)
	dir.Close()
	if err != nil {
		return err
	}
	for _, name := range names {
		if fd, err := strconv.Atoi(name); err == nil && fd > 2 {
			syscall.CloseOnExec(fd)
		}
	}
	return nil
}

// listenCaptainSeat binds the seat socket by its name inside its directory:
// a home's path can exceed the socket address limit, and the fence matches
// the resolved path either way.
func listenCaptainSeat(socket string) (*net.UnixListener, error) {
	wd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("captain seat: %w", err)
	}
	if err := os.Chdir(filepath.Dir(socket)); err != nil {
		return nil, fmt.Errorf("captain seat: %w", err)
	}
	defer os.Chdir(wd)
	name := filepath.Base(socket)
	if err := os.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("captain seat: removing a stale socket: %w", err)
	}
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: name, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("captain seat: %w", err)
	}
	l.SetUnlinkOnClose(false)
	if err := os.Chmod(name, 0o600); err != nil {
		l.Close()
		return nil, fmt.Errorf("captain seat: %w", err)
	}
	return l, nil
}

func serveCaptainSeatRequest(c *net.UnixConn, self, captainHome string, env []string) {
	defer c.Close()
	args, stdio, err := readCaptainSeatRequest(c)
	defer func() {
		for _, f := range stdio {
			f.Close()
		}
	}()
	if err == nil {
		err = captainSeatRequestError(args)
	}
	if err != nil {
		_ = json.NewEncoder(c).Encode(captainSeatReply{Exit: 1, Error: err.Error()})
		return
	}
	cmd := exec.Command(self, args...)
	cmd.Dir, cmd.Env = captainHome, env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdio[0], stdio[1], stdio[2]
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		_ = json.NewEncoder(c).Encode(captainSeatReply{Exit: 1, Error: fmt.Sprintf("captain seat: %v", err)})
		return
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	hangup := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, c)
		close(hangup)
	}()
	select {
	case err = <-done:
	case <-hangup:
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		err = <-done
	}
	code, err := exitCode(err)
	reply := captainSeatReply{Exit: code}
	if err != nil {
		reply = captainSeatReply{Exit: 1, Error: fmt.Sprintf("captain seat: %v", err)}
	}
	_ = json.NewEncoder(c).Encode(reply)
}

// readCaptainSeatRequest reads the length-prefixed request, whose first bytes
// carry the requester's three stdio descriptors.
func readCaptainSeatRequest(c *net.UnixConn) ([]string, []*os.File, error) {
	var head [4]byte
	oob := make([]byte, syscall.CmsgSpace(3*4))
	n, oobn, _, _, err := c.ReadMsgUnix(head[:], oob)
	if err != nil {
		return nil, nil, fmt.Errorf("captain seat: reading the request: %w", err)
	}
	var stdio []*os.File
	if msgs, err := syscall.ParseSocketControlMessage(oob[:oobn]); err == nil && len(msgs) == 1 {
		if fds, err := syscall.ParseUnixRights(&msgs[0]); err == nil {
			for i, fd := range fds {
				syscall.CloseOnExec(fd)
				stdio = append(stdio, os.NewFile(uintptr(fd), fmt.Sprintf("seat-stdio-%d", i)))
			}
		}
	}
	if len(stdio) != 3 {
		return nil, stdio, fmt.Errorf("captain seat: the request carries %d descriptors, want stdin, stdout and stderr", len(stdio))
	}
	if _, err := io.ReadFull(c, head[n:]); err != nil {
		return nil, stdio, fmt.Errorf("captain seat: reading the request: %w", err)
	}
	size := binary.BigEndian.Uint32(head[:])
	if size > captainSeatMaxRequest {
		return nil, stdio, fmt.Errorf("captain seat: a %d-byte request exceeds %d bytes", size, captainSeatMaxRequest)
	}
	body := make([]byte, size)
	if _, err := io.ReadFull(c, body); err != nil {
		return nil, stdio, fmt.Errorf("captain seat: reading the request: %w", err)
	}
	var req captainSeatRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, stdio, fmt.Errorf("captain seat: decoding the request: %w", err)
	}
	return req.Args, stdio, nil
}

// ForwardCaptainSeat sends args and this process's stdio to the captain's seat
// and returns the exit code of the command the seat ran.
func ForwardCaptainSeat(socket string, args []string) (int, error) {
	if err := os.Chdir(filepath.Dir(socket)); err != nil {
		return 0, fmt.Errorf("captain seat: %w", err)
	}
	c, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: filepath.Base(socket), Net: "unix"})
	if err != nil {
		return 0, fmt.Errorf("captain seat: %w", err)
	}
	defer c.Close()
	body, err := json.Marshal(captainSeatRequest{Args: args})
	if err != nil {
		return 0, fmt.Errorf("captain seat: %w", err)
	}
	var head [4]byte
	binary.BigEndian.PutUint32(head[:], uint32(len(body)))
	if _, _, err := c.WriteMsgUnix(head[:], syscall.UnixRights(0, 1, 2), nil); err != nil {
		return 0, fmt.Errorf("captain seat: sending the request: %w", err)
	}
	if _, err := c.Write(body); err != nil {
		return 0, fmt.Errorf("captain seat: sending the request: %w", err)
	}
	var reply captainSeatReply
	if err := json.NewDecoder(c).Decode(&reply); err != nil {
		return 0, fmt.Errorf("captain seat: reading the reply: %w", err)
	}
	if reply.Error != "" {
		return reply.Exit, errors.New(reply.Error)
	}
	return reply.Exit, nil
}

// exitCode is a waited process's exit code, 128+signal for a signalled one.
func exitCode(err error) (int, error) {
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		return 0, err
	}
	if ws, ok := exit.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal()), nil
	}
	return exit.ExitCode(), nil
}

func withoutEnv(env []string, key string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if !strings.HasPrefix(kv, key+"=") {
			out = append(out, kv)
		}
	}
	return out
}

// captainSeatRequestError refuses a request the seat does not run: a command
// that is not a seat command, or one that names another home. The seat runs
// each command in its captain home with its own environment.
func captainSeatRequestError(args []string) error {
	if !CaptainSeatRuns(args) {
		return fmt.Errorf("captain seat: munsu %s runs only inside the captain's fence", strings.Join(args, " "))
	}
	for _, a := range args {
		if a == "--home" || strings.HasPrefix(a, "--home=") {
			return fmt.Errorf("captain seat: --home is refused; the seat runs every command in its captain home")
		}
	}
	return nil
}
