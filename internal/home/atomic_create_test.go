package home

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

func TestAtomicCreatePublishesWithoutReplacing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "artifact")
	if err := AtomicCreate(path, []byte("anchored bytes"), 0640); err != nil {
		t.Fatalf("AtomicCreate: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "anchored bytes" {
		t.Fatalf("content = %q, %v; want anchored bytes", got, err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0640 {
			t.Fatalf("mode = %v, %v; want 0640", info, err)
		}
	}
	if err := AtomicCreate(path, []byte("replacement"), 0600); !errors.Is(err, os.ErrExist) {
		t.Fatalf("AtomicCreate on existing target = %v, want os.ErrExist", err)
	}
	got, err = os.ReadFile(path)
	if err != nil || string(got) != "anchored bytes" {
		t.Fatalf("existing target after refusal = %q, %v; want original content", got, err)
	}
	if leftovers, _ := filepath.Glob(filepath.Join(dir, ".home-create-*")); len(leftovers) != 0 {
		t.Fatalf("temporary files left behind: %v", leftovers)
	}
}

func TestAtomicCreateRefusesSymlinkTarget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	path := filepath.Join(dir, "artifact")
	if err := os.WriteFile(target, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := AtomicCreate(path, []byte("replacement"), 0600); !errors.Is(err, os.ErrExist) {
		t.Fatalf("AtomicCreate on symlink target = %v, want os.ErrExist", err)
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != "outside" {
		t.Fatalf("symlink target = %q, %v; want unchanged", got, err)
	}
}

func TestAtomicCreateConcurrentWritersHaveOneWinner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "artifact")
	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, content := range []string{"first", "second"} {
		wg.Add(1)
		go func(content string) {
			defer wg.Done()
			<-start
			results <- AtomicCreate(path, []byte(content), 0600)
		}(content)
	}
	close(start)
	wg.Wait()
	close(results)
	var successes, exists int
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, os.ErrExist):
			exists++
		default:
			t.Fatalf("AtomicCreate concurrent result = %v", err)
		}
	}
	if successes != 1 || exists != 1 {
		t.Fatalf("successes=%d exists=%d, want exactly one winner", successes, exists)
	}
	got, err := os.ReadFile(path)
	if err != nil || (string(got) != "first" && string(got) != "second") {
		t.Fatalf("published content = %q, %v; want one complete writer's bytes", got, err)
	}
}

func TestAtomicCreateRefusesUnsyncedTempWithoutPublishing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "artifact")
	syncErr := errors.New("injected file sync failure")
	orig := syncTemp
	syncTemp = func(*os.File) error { return syncErr }
	t.Cleanup(func() { syncTemp = orig })

	if err := AtomicCreate(path, []byte("not durable"), 0600); !errors.Is(err, syncErr) {
		t.Fatalf("AtomicCreate error = %v, want sync failure", err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("target exists after failed temp sync: %v", err)
	}
	if leftovers, _ := filepath.Glob(filepath.Join(dir, ".home-create-*")); len(leftovers) != 0 {
		t.Fatalf("temporary files left behind: %v", leftovers)
	}
}

func TestAtomicCreateKeepsPublishedTargetAfterDurabilityError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "artifact")
	durabilityErr := errors.New("injected post-publication durability failure")
	orig := atomicCreatePostPublish
	atomicCreatePostPublish = func(string) error { return durabilityErr }
	t.Cleanup(func() { atomicCreatePostPublish = orig })

	if err := AtomicCreate(path, []byte("published"), 0600); !errors.Is(err, durabilityErr) {
		t.Fatalf("AtomicCreate error = %v, want post-publication durability failure", err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "published" {
		t.Fatalf("published target = %q, %v; want preserved complete content", got, err)
	}
	if err := AtomicCreate(path, []byte("retry"), 0600); !errors.Is(err, os.ErrExist) {
		t.Fatalf("retry over uncertain existing target = %v, want os.ErrExist", err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "published" {
		t.Fatalf("target after retry = %q, %v; retry must not remove or replace it", got, err)
	}
	if leftovers, _ := filepath.Glob(filepath.Join(dir, ".home-create-*")); len(leftovers) != 0 {
		t.Fatalf("temporary files left behind: %v", leftovers)
	}
}
