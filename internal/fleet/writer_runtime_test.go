package fleet

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

func TestDurableArtifactScannerReadsGenericIdentity(t *testing.T) {
	h := t.TempDir()
	canonical, err := home.CanonicalPath(h)
	if err != nil {
		t.Fatal(err)
	}
	id := home.WriterIdentity{SchemaVersion: 1, Kind: "watcher", PID: 42, StartToken: "123", ExecutablePath: "/bin/munsu", CanonicalHome: canonical}
	if err := home.PublishWriterIdentity(h, "watcher", id); err != nil {
		t.Fatal(err)
	}
	artifacts, err := (DurableArtifactScanner{Kinds: []string{"watcher"}}).Scan(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) != 1 || artifacts[0].PID != 42 || artifacts[0].StartToken != "123" {
		t.Fatalf("artifacts=%+v", artifacts)
	}
}
func TestDurableArtifactScannerRejectsCorruptIdentity(t *testing.T) {
	h := t.TempDir()
	if err := os.MkdirAll(h+"/state", 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(home.WriterIdentityPath(h, "watcher"), []byte("bad"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := (DurableArtifactScanner{Kinds: []string{"watcher"}}).Scan(h); err == nil {
		t.Fatal("expected error")
	}
}
func TestWriterKindForArgsParsesHomeFormsAndCanonicalAliases(t *testing.T) {
	h := t.TempDir()
	if err := os.MkdirAll(filepath.Join(h, "data"), 0700); err != nil {
		t.Fatal(err)
	}
	canonical, err := home.CanonicalPath(h)
	if err != nil {
		t.Fatal(err)
	}
	alias := h + string(os.PathSeparator) + "."
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"separate", []string{"munsu", "watch", "--home", h}, "watcher"},
		{"equals", []string{"munsu", "afk", "--home=" + h}, "afk"},
		{"alias", []string{"munsu", "watch", "--home", alias}, "watcher"},
		{"substring", []string{"munsu", "watch", "--note", h, "--home", t.TempDir()}, ""},
		{"unrelated", []string{"echo", h, "watch"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := writerKindForArgs(tc.args, canonical); got != tc.want {
				t.Fatalf("got=%q want=%q", got, tc.want)
			}
		})
	}
}

func TestDarwinCommandSplitterPreservesQuotedHome(t *testing.T) {
	if got := splitProcessCommand(`munsu watch --home "/tmp/home with spaces"`); len(got) != 4 || got[3] != "/tmp/home with spaces" {
		t.Fatalf("args=%q", got)
	}
}

func TestInspectCurrentProcessReturnsStableIdentity(t *testing.T) {
	first, err := inspectProcess(os.Getpid())
	if err != nil {
		t.Skipf("process identity unavailable: %v", err)
	}
	second, err := inspectProcess(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if first != second || first.StartToken == "" || first.ExecutablePath == "" {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
}
