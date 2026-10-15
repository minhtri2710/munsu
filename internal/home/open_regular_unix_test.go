package home

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A fenced soldier writes its own status file and ack log, so it can put a
// symlink (here to another file inside state/, which validateStatePath
// allows), a FIFO or a directory at those names. Every unfenced funnel over
// them refuses each one without blocking, and reads or appends the nearest
// valid case, a regular file. A FIFO row that blocked fails on its timer.
func TestSoldierWritableFilesOpenOnlyAsRegularFiles(t *testing.T) {
	const decision = "needs-decision: pick one [key=k]"
	const ack = `{"schema_version":"1","message_id":"m1","sender_rank":"general","sender_identity":"general","receiver_rank":"soldier","receiver_id":"t1","task_id":"t1","payload_hash":"h","processed_at":1,"outcome":"accepted"}`
	funnels := []struct {
		name    string
		file    string // the soldier-writable file under state/
		content string // a regular file's content, and the link target's
		run     func(home string) (string, error)
		want    string // in the result for the regular file
		refused string // the result of a refusal its funnel swallows; "" when it returns the error
	}{
		{"status append", "t1.status", decision, func(home string) (string, error) {
			if err := AppendStatus(home, "t1", "working: x"); err != nil {
				return "", err
			}
			b, err := os.ReadFile(filepath.Join(StateDir(home), "t1.status"))
			return string(b), err
		}, "working: x", ""},
		{"status read", "t1.status", decision, func(home string) (string, error) {
			lines, err := ReadStatus(home, "t1")
			return strings.Join(lines, "\n"), err
		}, decision, ""},
		{"raw read", "t1.status", decision, func(home string) (string, error) {
			b, err := ReadRegularFile(filepath.Join(StateDir(home), "t1.status"))
			return string(b), err
		}, decision, ""},
		{"open decisions", "t1.status", decision, func(home string) (string, error) {
			return fmt.Sprint(len(OpenDecisions(filepath.Join(StateDir(home), "t1.status")))), nil
		}, "1", "0"},
		{"ack read", "t1.acks", ack, func(home string) (string, error) {
			got, err := NewStore(home).ReadAck(&Envelope{MessageID: "m1", SenderIdentity: "general", ReceiverRank: RankSoldier, TaskID: "t1"})
			if got == nil {
				return "", err
			}
			return got.MessageID, err
		}, "m1", ""},
	}
	plants := []struct {
		name   string
		plant  func(path, other, content string) error
		refuse string // "" for the allowed regular file
	}{
		{"a symlink to another state file", func(path, other, _ string) error { return os.Symlink(other, path) }, "is a symlink"},
		{"a FIFO", func(path, _, _ string) error { return syscall.Mkfifo(path, 0o600) }, "is not a regular file"},
		{"a directory", func(path, _, _ string) error { return os.Mkdir(path, 0o700) }, "is not a regular file"},
		{"a regular file", func(path, _, content string) error { return os.WriteFile(path, []byte(content+"\n"), 0o600) }, ""},
	}
	for _, fn := range funnels {
		for _, pl := range plants {
			t.Run(fn.name+" of "+pl.name, func(t *testing.T) {
				home := t.TempDir()
				state := StateDir(home)
				if err := os.Mkdir(state, 0o700); err != nil {
					t.Fatal(err)
				}
				other := filepath.Join(state, "other")
				if err := os.WriteFile(other, []byte(fn.content+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := pl.plant(filepath.Join(state, fn.file), other, fn.content); err != nil {
					t.Fatal(err)
				}
				type result struct {
					got string
					err error
				}
				done := make(chan result, 1)
				go func() {
					got, err := fn.run(home)
					done <- result{got, err}
				}()
				var res result
				select {
				case res = <-done:
				case <-time.After(5 * time.Second):
					t.Fatalf("%s blocked on %s", fn.name, pl.name)
				}
				switch {
				case pl.refuse == "":
					if res.err != nil || !strings.Contains(res.got, fn.want) {
						t.Fatalf("got %q, %v; want the regular file read or appended (%q)", res.got, res.err, fn.want)
					}
				case fn.refused != "":
					if res.got != fn.refused {
						t.Fatalf("got %q, want %q: the refused file read as nothing", res.got, fn.refused)
					}
				default:
					if res.err == nil || !strings.Contains(res.err.Error(), pl.refuse) {
						t.Fatalf("err = %v, want %q", res.err, pl.refuse)
					}
				}
				if b, _ := os.ReadFile(other); string(b) != fn.content+"\n" {
					t.Fatalf("the link's target now reads %q", b)
				}
			})
		}
	}
}
