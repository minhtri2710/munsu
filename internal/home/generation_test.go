package home

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestGenerationRootRejectsUnsafeGeneration(t *testing.T) {
	homeDir := t.TempDir()
	for _, generation := range []string{"", ".", "..", "../escape", "a/b", `a\b`} {
		if _, err := GenerationRoot(homeDir, generation); err == nil {
			t.Fatalf("GenerationRoot(%q) succeeded, want error", generation)
		}
	}
}

func TestGenerationRootIsContainedUnderHome(t *testing.T) {
	homeDir := t.TempDir()
	got, err := GenerationRoot(homeDir, "gen-20260727")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(homeDir, GenerationsDirName, "gen-20260727")
	if got != want {
		t.Fatalf("GenerationRoot() = %q, want %q", got, want)
	}
}

func TestActivationRecordRoundTripAndResolve(t *testing.T) {
	homeDir := t.TempDir()
	record := ActivationRecord{
		SchemaVersion: 1,
		Generation:    "gen-20260727",
		BuildIdentity: "build-abc",
	}
	root, err := GenerationRoot(homeDir, record.Generation)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := PublishActivation(homeDir, record); err != nil {
		t.Fatal(err)
	}

	got, err := ReadActivation(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	if got != record {
		t.Fatalf("ReadActivation() = %+v, want %+v", got, record)
	}

	activeRoot, err := ResolveActiveRoot(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := GenerationRoot(homeDir, record.Generation)
	if activeRoot != want {
		t.Fatalf("ResolveActiveRoot() = %q, want %q", activeRoot, want)
	}
}

func TestPublishActivationRequiresExistingGenerationRoot(t *testing.T) {
	homeDir := t.TempDir()
	err := PublishActivation(homeDir, ActivationRecord{SchemaVersion: 1, Generation: "missing", BuildIdentity: "build"})
	if err == nil {
		t.Fatal("PublishActivation succeeded without generation root")
	}
	if _, statErr := os.Stat(filepath.Join(homeDir, ActivationRecordName)); !os.IsNotExist(statErr) {
		t.Fatalf("activation record should not exist after failed publication: %v", statErr)
	}
}

func TestPublishActivationRejectsReplacement(t *testing.T) {
	homeDir := t.TempDir()
	for _, generation := range []string{"gen-one", "gen-two"} {
		root, err := GenerationRoot(homeDir, generation)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(root, 0700); err != nil {
			t.Fatal(err)
		}
	}
	first := ActivationRecord{SchemaVersion: 1, Generation: "gen-one", BuildIdentity: "build-one"}
	if err := PublishActivation(homeDir, first); err != nil {
		t.Fatal(err)
	}
	if err := PublishActivation(homeDir, ActivationRecord{SchemaVersion: 1, Generation: "gen-two", BuildIdentity: "build-two"}); err == nil {
		t.Fatal("PublishActivation replaced an existing activation record")
	}
	got, err := ReadActivation(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	if got != first {
		t.Fatalf("activation changed after rejected replacement: %+v", got)
	}
}

func TestResolveActiveRootWithoutActivation(t *testing.T) {
	_, err := ResolveActiveRoot(t.TempDir())
	if !errors.Is(err, ErrNotActivated) {
		t.Fatalf("ResolveActiveRoot() error = %v, want ErrNotActivated", err)
	}
}

func TestResolveActiveRootRejectsMissingGenerationRoot(t *testing.T) {
	homeDir := t.TempDir()
	root, err := GenerationRoot(homeDir, "missing-later")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := PublishActivation(homeDir, ActivationRecord{SchemaVersion: 1, Generation: "missing-later", BuildIdentity: "build"}); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveActiveRoot(homeDir); err == nil {
		t.Fatal("ResolveActiveRoot accepted a missing active generation root")
	}
}

func TestPublishActivationValidatesRequiredFields(t *testing.T) {
	homeDir := t.TempDir()
	root, err := GenerationRoot(homeDir, "gen")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	for _, record := range []ActivationRecord{
		{SchemaVersion: 0, Generation: "gen", BuildIdentity: "build"},
		{SchemaVersion: 1, Generation: "gen", BuildIdentity: ""},
	} {
		if err := PublishActivation(homeDir, record); err == nil {
			t.Fatalf("PublishActivation(%+v) succeeded", record)
		}
	}
}

func TestPublishActivationConcurrentExactlyOneWinner(t *testing.T) {
	homeDir := t.TempDir()
	for _, generation := range []string{"gen-one", "gen-two"} {
		root, err := GenerationRoot(homeDir, generation)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(root, 0700); err != nil {
			t.Fatal(err)
		}
	}
	records := []ActivationRecord{{SchemaVersion: 1, Generation: "gen-one", BuildIdentity: "one"}, {SchemaVersion: 1, Generation: "gen-two", BuildIdentity: "two"}}
	start := make(chan struct{})
	results := make(chan error, len(records))
	var wg sync.WaitGroup
	for _, record := range records {
		wg.Add(1)
		go func(record ActivationRecord) {
			defer wg.Done()
			<-start
			results <- PublishActivation(homeDir, record)
		}(record)
	}
	close(start)
	wg.Wait()
	close(results)
	wins := 0
	for err := range results {
		if err == nil {
			wins++
		}
	}
	if wins != 1 {
		t.Fatalf("successful publications = %d, want 1", wins)
	}
	if _, err := ReadActivation(homeDir); err != nil {
		t.Fatalf("winning activation is unreadable: %v", err)
	}
}

func TestActivationRecordIsUserPrivate(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("Unix permission bits are not the Windows ACL contract")
	}
	homeDir := t.TempDir()
	root, err := GenerationRoot(homeDir, "gen")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := PublishActivation(homeDir, ActivationRecord{SchemaVersion: 1, Generation: "gen", BuildIdentity: "build"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(homeDir, ActivationRecordName))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Fatalf("activation permissions = %04o, want 0600", got)
	}
}

func TestReadActivationRejectsCorruptRecord(t *testing.T) {
	homeDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(homeDir, ActivationRecordName), []byte("not-json\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadActivation(homeDir); err == nil {
		t.Fatal("ReadActivation accepted corrupt JSON")
	}
}
