//go:build integration

package fleet

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeRevision returns a stable fake git revision for tests.
func fakeRevision(suffix string) string {
	return "00000000000000000000000000000000000000" + suffix
}

func TestContextManifest_NewWithDefaults(t *testing.T) {
	m := NewContextManifest("Implement the payment module", DefaultContextManifestBudgets())
	if m == nil {
		t.Fatal("expected non-nil manifest")
	}
	if m.ManifestVersion != ContextManifestVersion {
		t.Errorf("ManifestVersion = %q, want %q", m.ManifestVersion, ContextManifestVersion)
	}
	if m.Revision != 1 {
		t.Errorf("Revision = %d, want 1", m.Revision)
	}
	if m.AuthorHint != "Implement the payment module" {
		t.Errorf("AuthorHint = %q", m.AuthorHint)
	}
	if len(m.Entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(m.Entries))
	}
	if m.Stale {
		t.Error("expected Stale=false on new manifest")
	}
	b := m.Budgets
	if b.MaxImplementation != 5 || b.MaxTest != 3 || b.MaxArchitecture != 2 {
		t.Errorf("unexpected budgets: impl=%d test=%d arch=%d", b.MaxImplementation, b.MaxTest, b.MaxArchitecture)
	}
}

func TestContextManifest_AddEntryWithinBudget(t *testing.T) {
	m := NewContextManifest("Refactor auth", DefaultContextManifestBudgets())
	err := m.AddEntry("internal/auth/auth.go", "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789", fakeRevision("01"), ContextCategoryImplementation, "Core authentication logic")
	if err != nil {
		t.Fatalf("AddEntry: %v", err)
	}
	if len(m.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(m.Entries))
	}
	if m.Entries[0].Path != "internal/auth/auth.go" {
		t.Errorf("Path = %q", m.Entries[0].Path)
	}
	if m.Entries[0].Category != ContextCategoryImplementation {
		t.Errorf("Category = %q", m.Entries[0].Category)
	}
	if m.Entries[0].Reason != "Core authentication logic" {
		t.Errorf("Reason = %q", m.Entries[0].Reason)
	}
	if m.Entries[0].Stale {
		t.Error("expected Stale=false on fresh entry")
	}
}

func TestContextManifest_BudgetExceeded(t *testing.T) {
	m := NewContextManifest("Test budgets", ContextManifestBudgets{
		MaxImplementation: 1,
		MaxTest:           1,
		MaxArchitecture:   1,
	})

	// First entry should succeed.
	if err := m.AddEntry("file1.go", "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789", fakeRevision("01"), ContextCategoryImplementation, "reason 1"); err != nil {
		t.Fatalf("first entry: %v", err)
	}

	// Second entry should fail due to budget.
	err := m.AddEntry("file2.go", "1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef", fakeRevision("02"), ContextCategoryImplementation, "reason 2")
	if err == nil {
		t.Fatal("expected error for exceeded budget")
	}
	if !strings.Contains(err.Error(), "budget exceeded") {
		t.Errorf("unexpected error: %v", err)
	}
	if len(m.Entries) != 1 {
		t.Errorf("expected 1 entry, got %d", len(m.Entries))
	}
}

func TestContextManifest_BudgetExceeded_AllCategories(t *testing.T) {
	m := NewContextManifest("Test all budgets", ContextManifestBudgets{
		MaxImplementation: 1,
		MaxTest:           1,
		MaxArchitecture:   1,
	})

	dig := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	dig2 := "1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"
	dig3 := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	dig4 := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	// Fill each category exactly to budget.
	if err := m.AddEntry("impl.go", dig, fakeRevision("01"), ContextCategoryImplementation, "impl"); err != nil {
		t.Fatal(err)
	}
	if err := m.AddEntry("impl_test.go", dig2, fakeRevision("02"), ContextCategoryTest, "test"); err != nil {
		t.Fatal(err)
	}
	if err := m.AddEntry("arch.md", dig3, fakeRevision("03"), ContextCategoryArchitecture, "arch"); err != nil {
		t.Fatal(err)
	}

	// All three categories should now reject.
	if err := m.AddEntry("impl2.go", dig4, fakeRevision("04"), ContextCategoryImplementation, "impl2"); err == nil {
		t.Error("expected budget exceeded for implementation")
	}
	if err := m.AddEntry("impl2_test.go", dig4, fakeRevision("05"), ContextCategoryTest, "test2"); err == nil {
		t.Error("expected budget exceeded for test")
	}
	if err := m.AddEntry("arch2.md", dig4, fakeRevision("06"), ContextCategoryArchitecture, "arch2"); err == nil {
		t.Error("expected budget exceeded for architecture")
	}
}

func TestContextManifest_DuplicateEntry(t *testing.T) {
	m := NewContextManifest("No dupes", DefaultContextManifestBudgets())
	dig := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	if err := m.AddEntry("same.go", dig, fakeRevision("01"), ContextCategoryImplementation, "first"); err != nil {
		t.Fatal(err)
	}
	err := m.AddEntry("same.go", dig, fakeRevision("02"), ContextCategoryImplementation, "second")
	if err == nil {
		t.Fatal("expected error for duplicate path")
	}
	if !strings.Contains(err.Error(), "duplicate entry") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestContextManifest_AddEntry_Validation(t *testing.T) {
	m := NewContextManifest("validation", DefaultContextManifestBudgets())
	dig := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	rev := fakeRevision("01")

	// Empty path.
	if err := m.AddEntry("", dig, rev, ContextCategoryImplementation, "reason"); err == nil {
		t.Error("expected error for empty path")
	}

	// Invalid digest.
	if err := m.AddEntry("f.go", "short", rev, ContextCategoryImplementation, "reason"); err == nil {
		t.Error("expected error for invalid digest")
	}

	// Empty revision.
	if err := m.AddEntry("f.go", dig, "", ContextCategoryImplementation, "reason"); err == nil {
		t.Error("expected error for empty revision")
	}

	// Empty reason.
	if err := m.AddEntry("f.go", dig, rev, ContextCategoryImplementation, ""); err == nil {
		t.Error("expected error for empty reason")
	}
}

func TestContextManifest_WriteAndRead(t *testing.T) {
	tmp := t.TempDir()
	m := NewContextManifest("Implement payment", DefaultContextManifestBudgets())
	dig := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	dig2 := "1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"

	if err := m.AddEntry("internal/payment/processor.go", dig, fakeRevision("01"), ContextCategoryImplementation, "Payment processor logic"); err != nil {
		t.Fatal(err)
	}
	if err := m.AddEntry("internal/payment/processor_test.go", dig2, fakeRevision("02"), ContextCategoryTest, "Payment processor tests"); err != nil {
		t.Fatal(err)
	}

	writeDigest, err := WriteContextManifest(tmp, m)
	if err != nil {
		t.Fatalf("WriteContextManifest: %v", err)
	}
	if len(writeDigest) != 64 {
		t.Errorf("write digest length = %d, want 64", len(writeDigest))
	}

	got, err := ReadContextManifest(tmp)
	if err != nil {
		t.Fatalf("ReadContextManifest: %v", err)
	}
	if got.ManifestVersion != ContextManifestVersion {
		t.Errorf("ManifestVersion = %q", got.ManifestVersion)
	}
	if got.Revision != 1 {
		t.Errorf("Revision = %d, want 1", got.Revision)
	}
	if got.AuthorHint != "Implement payment" {
		t.Errorf("AuthorHint = %q", got.AuthorHint)
	}
	if len(got.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(got.Entries))
	}
	if got.Entries[0].Path != "internal/payment/processor.go" {
		t.Errorf("entry[0].Path = %q", got.Entries[0].Path)
	}
	if got.Entries[0].Category != ContextCategoryImplementation {
		t.Errorf("entry[0].Category = %q", got.Entries[0].Category)
	}
	if got.Entries[1].Path != "internal/payment/processor_test.go" {
		t.Errorf("entry[1].Path = %q", got.Entries[1].Path)
	}
	if got.Entries[1].Category != ContextCategoryTest {
		t.Errorf("entry[1].Category = %q", got.Entries[1].Category)
	}
}

func TestContextManifest_WriteAndRead_EmptyEntries(t *testing.T) {
	tmp := t.TempDir()
	m := NewContextManifest("Empty manifest", DefaultContextManifestBudgets())
	_, err := WriteContextManifest(tmp, m)
	if err != nil {
		t.Fatalf("WriteContextManifest: %v", err)
	}
	got, err := ReadContextManifest(tmp)
	if err != nil {
		t.Fatalf("ReadContextManifest: %v", err)
	}
	if got.Revision != 1 {
		t.Errorf("Revision = %d, want 1", got.Revision)
	}
	if len(got.Entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(got.Entries))
	}
}

func TestContextManifest_WriteReturnsDigest(t *testing.T) {
	tmp := t.TempDir()
	m := NewContextManifest("Deterministic write", DefaultContextManifestBudgets())
	dig1, err := WriteContextManifest(tmp, m)
	if err != nil {
		t.Fatal(err)
	}

	// Write again to same dir — should produce same digest.
	dig2, err := WriteContextManifest(tmp, m)
	if err != nil {
		t.Fatal(err)
	}
	if dig1 != dig2 {
		t.Errorf("deterministic write produced different digests: %s != %s", dig1, dig2)
	}

	// Verify the digest matches the actual file.
	data, err := os.ReadFile(filepath.Join(tmp, ContextManifestName))
	if err != nil {
		t.Fatal(err)
	}
	if sha256Content(data) != dig1 {
		t.Error("returned digest does not match written file")
	}
}

func TestContextManifest_CheckStale_NoChange(t *testing.T) {
	tmp := t.TempDir()
	content := []byte("package main\nfunc main() {}\n")
	filePath := "main.go"
	if err := os.WriteFile(filepath.Join(tmp, filePath), content, 0644); err != nil {
		t.Fatal(err)
	}

	m := NewContextManifest("Stale check", DefaultContextManifestBudgets())
	dig := sha256Content(content)
	if err := m.AddEntry(filePath, dig, fakeRevision("01"), ContextCategoryImplementation, "entry point"); err != nil {
		t.Fatal(err)
	}

	if err := m.CheckStale(tmp); err != nil {
		t.Fatalf("CheckStale: %v", err)
	}
	if m.Stale {
		t.Error("expected Stale=false when content unchanged")
	}
	if m.Entries[0].Stale {
		t.Error("expected entry Stale=false when content unchanged")
	}
}

func TestContextManifest_CheckStale_ContentChanged(t *testing.T) {
	tmp := t.TempDir()
	original := []byte("package main\nfunc main() {}\n")
	filePath := "main.go"
	if err := os.WriteFile(filepath.Join(tmp, filePath), original, 0644); err != nil {
		t.Fatal(err)
	}

	m := NewContextManifest("Stale detection", DefaultContextManifestBudgets())
	dig := sha256Content(original)
	if err := m.AddEntry(filePath, dig, fakeRevision("01"), ContextCategoryImplementation, "entry point"); err != nil {
		t.Fatal(err)
	}

	// Change the file content.
	modified := []byte("package main\nfunc main() { println(\"hello\") }\n")
	if err := os.WriteFile(filepath.Join(tmp, filePath), modified, 0644); err != nil {
		t.Fatal(err)
	}

	if err := m.CheckStale(tmp); err != nil {
		t.Fatalf("CheckStale: %v", err)
	}
	if !m.Stale {
		t.Error("expected Stale=true when content changed")
	}
	if !m.Entries[0].Stale {
		t.Error("expected entry Stale=true when content changed")
	}
}

func TestContextManifest_CheckStale_FileMissing(t *testing.T) {
	tmp := t.TempDir()
	m := NewContextManifest("Missing file", DefaultContextManifestBudgets())
	dig := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	if err := m.AddEntry("missing.go", dig, fakeRevision("01"), ContextCategoryImplementation, "missing file"); err != nil {
		t.Fatal(err)
	}

	if err := m.CheckStale(tmp); err != nil {
		t.Fatalf("CheckStale: %v", err)
	}
	if !m.Stale {
		t.Error("expected Stale=true when file missing")
	}
	if !m.Entries[0].Stale {
		t.Error("expected entry Stale=true when file missing")
	}
}

func TestContextManifest_CheckStale_PartialStale(t *testing.T) {
	tmp := t.TempDir()
	file1 := "stable.go"
	file2 := "changed.go"
	content1 := []byte("package main\n")
	content2 := []byte("package main\nfunc f() {}\n")

	if err := os.WriteFile(filepath.Join(tmp, file1), content1, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, file2), content2, 0644); err != nil {
		t.Fatal(err)
	}

	m := NewContextManifest("Partial stale", DefaultContextManifestBudgets())
	if err := m.AddEntry(file1, sha256Content(content1), fakeRevision("01"), ContextCategoryImplementation, "stable"); err != nil {
		t.Fatal(err)
	}
	if err := m.AddEntry(file2, sha256Content(content2), fakeRevision("02"), ContextCategoryTest, "changed"); err != nil {
		t.Fatal(err)
	}

	// Change only file2.
	modified := []byte("package main\nfunc f() { println() }\n")
	if err := os.WriteFile(filepath.Join(tmp, file2), modified, 0644); err != nil {
		t.Fatal(err)
	}

	if err := m.CheckStale(tmp); err != nil {
		t.Fatalf("CheckStale: %v", err)
	}
	if !m.Stale {
		t.Error("expected Stale=true when one entry stale")
	}
	if m.Entries[0].Stale {
		t.Error("expected entry[0] Stale=false (unchanged)")
	}
	if !m.Entries[1].Stale {
		t.Error("expected entry[1] Stale=true (changed)")
	}
}

func TestContextManifest_Expand_CreatesNewRevision(t *testing.T) {
	m := NewContextManifest("Expand test", DefaultContextManifestBudgets())
	if m.Revision != 1 {
		t.Fatalf("expected revision 1, got %d", m.Revision)
	}

	m.Expand(nil)
	if m.Revision != 2 {
		t.Errorf("expected revision 2 after expand, got %d", m.Revision)
	}

	// Budgets should be unchanged.
	if m.Budgets.MaxImplementation != 5 {
		t.Errorf("expected budgets unchanged, got MaxImplementation=%d", m.Budgets.MaxImplementation)
	}
}

func TestContextManifest_Expand_WithNewBudgets(t *testing.T) {
	m := NewContextManifest("Expand with new budgets", DefaultContextManifestBudgets())
	m.Expand(&ContextManifestBudgets{
		MaxImplementation: 10,
		MaxTest:           6,
		MaxArchitecture:   4,
	})
	if m.Revision != 2 {
		t.Errorf("expected revision 2, got %d", m.Revision)
	}
	if m.Budgets.MaxImplementation != 10 {
		t.Errorf("MaxImplementation = %d, want 10", m.Budgets.MaxImplementation)
	}
	if m.Budgets.MaxTest != 6 {
		t.Errorf("MaxTest = %d, want 6", m.Budgets.MaxTest)
	}
	if m.Budgets.MaxArchitecture != 4 {
		t.Errorf("MaxArchitecture = %d, want 4", m.Budgets.MaxArchitecture)
	}
}

func TestContextManifest_Expand_Multiple(t *testing.T) {
	m := NewContextManifest("Multiple expansions", DefaultContextManifestBudgets())
	m.Expand(nil)
	m.Expand(nil)
	m.Expand(nil)
	if m.Revision != 4 {
		t.Errorf("expected revision 4 after 3 expands, got %d", m.Revision)
	}
}

func TestContextManifest_ExpandThenWriteAndRead(t *testing.T) {
	tmp := t.TempDir()
	m := NewContextManifest("Expanded", DefaultContextManifestBudgets())
	dig := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	if err := m.AddEntry("impl.go", dig, fakeRevision("01"), ContextCategoryImplementation, "impl"); err != nil {
		t.Fatal(err)
	}
	m.Expand(&ContextManifestBudgets{
		MaxImplementation: 10,
		MaxTest:           5,
		MaxArchitecture:   3,
	})

	writeDigest, err := WriteContextManifest(tmp, m)
	if err != nil {
		t.Fatalf("WriteContextManifest: %v", err)
	}

	got, err := ReadContextManifest(tmp)
	if err != nil {
		t.Fatalf("ReadContextManifest: %v", err)
	}
	if got.Revision != 2 {
		t.Errorf("Revision = %d, want 2", got.Revision)
	}
	if got.Budgets.MaxImplementation != 10 {
		t.Errorf("MaxImplementation = %d, want 10", got.Budgets.MaxImplementation)
	}
	if got.Budgets.MaxTest != 5 {
		t.Errorf("MaxTest = %d, want 5", got.Budgets.MaxTest)
	}
	if got.Budgets.MaxArchitecture != 3 {
		t.Errorf("MaxArchitecture = %d, want 3", got.Budgets.MaxArchitecture)
	}

	// Verify digest matches.
	data, err := os.ReadFile(filepath.Join(tmp, ContextManifestName))
	if err != nil {
		t.Fatal(err)
	}
	if sha256Content(data) != writeDigest {
		t.Error("write digest does not match file")
	}
}

func TestContextManifest_Read_Missing(t *testing.T) {
	tmp := t.TempDir()
	_, err := ReadContextManifest(tmp)
	if err == nil {
		t.Error("expected error reading missing manifest")
	}
}

func TestContextManifest_Read_CorruptJSON(t *testing.T) {
	tmp := t.TempDir()
	writeContextManifestRaw(t, tmp, `{invalid json}`)
	_, err := ReadContextManifest(tmp)
	if err == nil {
		t.Error("expected error for corrupt JSON")
	}
}

func TestContextManifest_Read_TrailingData(t *testing.T) {
	tmp := t.TempDir()
	writeContextManifestRaw(t, tmp, `{"manifest_version":"context-manifest-v1","revision":1,"budgets":{"max_implementation":5,"max_test":3,"max_architecture":2},"entries":[]}{"extra":"data"}`)
	_, err := ReadContextManifest(tmp)
	if err == nil {
		t.Error("expected error for trailing data")
	}
	if !strings.Contains(err.Error(), "trailing data") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestContextManifest_Read_UnknownFields(t *testing.T) {
	tmp := t.TempDir()
	writeContextManifestRaw(t, tmp, `{"manifest_version":"context-manifest-v1","revision":1,"budgets":{"max_implementation":5,"max_test":3,"max_architecture":2},"entries":[],"unknown_field":"value"}`)
	_, err := ReadContextManifest(tmp)
	if err == nil {
		t.Error("expected error for unknown fields")
	}
}

func TestContextManifest_Read_UnknownVersion(t *testing.T) {
	tmp := t.TempDir()
	writeContextManifestRaw(t, tmp, `{"manifest_version":"context-manifest-v2","revision":1,"budgets":{"max_implementation":5,"max_test":3,"max_architecture":2},"entries":[]}`)
	_, err := ReadContextManifest(tmp)
	if err == nil {
		t.Error("expected error for unknown version")
	}
	if !strings.Contains(err.Error(), "unsupported version") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestContextManifest_Validate_Nil(t *testing.T) {
	err := ValidateContextManifest(nil)
	if err == nil {
		t.Error("expected error for nil manifest")
	}
}

func TestContextManifest_Validate_InvalidRevision(t *testing.T) {
	m := &ContextManifest{
		ManifestVersion: ContextManifestVersion,
		Revision:        0,
		Budgets:         DefaultContextManifestBudgets(),
	}
	err := ValidateContextManifest(m)
	if err == nil {
		t.Error("expected error for revision 0")
	}
	if !strings.Contains(err.Error(), "revision") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestContextManifest_Validate_InvalidBudget(t *testing.T) {
	m := &ContextManifest{
		ManifestVersion: ContextManifestVersion,
		Revision:        1,
		Budgets: ContextManifestBudgets{
			MaxImplementation: 0,
			MaxTest:           3,
			MaxArchitecture:   2,
		},
	}
	err := ValidateContextManifest(m)
	if err == nil {
		t.Error("expected error for MaxImplementation=0")
	}
	if !strings.Contains(err.Error(), "max_implementation") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestContextManifest_Validate_EntryBudgetExceeded(t *testing.T) {
	m := &ContextManifest{
		ManifestVersion: ContextManifestVersion,
		Revision:        1,
		Budgets: ContextManifestBudgets{
			MaxImplementation: 1,
			MaxTest:           3,
			MaxArchitecture:   2,
		},
		Entries: []ContextManifestEntry{
			{Path: "a.go", SHA256: "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789", Revision: fakeRevision("01"), Category: ContextCategoryImplementation, Reason: "first"},
			{Path: "b.go", SHA256: "1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef", Revision: fakeRevision("02"), Category: ContextCategoryImplementation, Reason: "second"},
		},
	}
	err := ValidateContextManifest(m)
	if err == nil {
		t.Error("expected error for exceeded budget")
	}
	if !strings.Contains(err.Error(), "exceeds budget") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestContextManifest_Validate_EmptyEntryPath(t *testing.T) {
	m := &ContextManifest{
		ManifestVersion: ContextManifestVersion,
		Revision:        1,
		Budgets:         DefaultContextManifestBudgets(),
		Entries: []ContextManifestEntry{
			{Path: "", SHA256: "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789", Revision: fakeRevision("01"), Category: ContextCategoryImplementation, Reason: "empty path"},
		},
	}
	err := ValidateContextManifest(m)
	if err == nil {
		t.Error("expected error for empty entry path")
	}
}

func TestContextManifest_Validate_EmptyEntryReason(t *testing.T) {
	m := &ContextManifest{
		ManifestVersion: ContextManifestVersion,
		Revision:        1,
		Budgets:         DefaultContextManifestBudgets(),
		Entries: []ContextManifestEntry{
			{Path: "a.go", SHA256: "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789", Revision: fakeRevision("01"), Category: ContextCategoryImplementation, Reason: ""},
		},
	}
	err := ValidateContextManifest(m)
	if err == nil {
		t.Error("expected error for empty entry reason")
	}
}

func TestContextManifest_Validate_UnknownCategory(t *testing.T) {
	m := &ContextManifest{
		ManifestVersion: ContextManifestVersion,
		Revision:        1,
		Budgets:         DefaultContextManifestBudgets(),
		Entries: []ContextManifestEntry{
			{Path: "a.go", SHA256: "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789", Revision: fakeRevision("01"), Category: "unknown", Reason: "weird"},
		},
	}
	err := ValidateContextManifest(m)
	if err == nil {
		t.Error("expected error for unknown category")
	}
	if !strings.Contains(err.Error(), "unknown category") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestContextManifest_Validate_Valid(t *testing.T) {
	m := &ContextManifest{
		ManifestVersion: ContextManifestVersion,
		Revision:        1,
		Budgets:         DefaultContextManifestBudgets(),
		Entries: []ContextManifestEntry{
			{Path: "impl.go", SHA256: "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789", Revision: fakeRevision("01"), Category: ContextCategoryImplementation, Reason: "core logic"},
			{Path: "impl_test.go", SHA256: "1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef", Revision: fakeRevision("02"), Category: ContextCategoryTest, Reason: "tests"},
			{Path: "ARCHITECTURE.md", SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Revision: fakeRevision("03"), Category: ContextCategoryArchitecture, Reason: "design doc"},
		},
	}
	err := ValidateContextManifest(m)
	if err != nil {
		t.Fatalf("expected valid manifest: %v", err)
	}
}

func TestContextManifest_WriteNil(t *testing.T) {
	_, err := WriteContextManifest(t.TempDir(), nil)
	if err == nil {
		t.Error("expected error writing nil manifest")
	}
}

func TestContextManifest_AddEntryOnNil(t *testing.T) {
	var m *ContextManifest
	err := m.AddEntry("f.go", "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789", fakeRevision("01"), ContextCategoryImplementation, "reason")
	if err == nil {
		t.Error("expected error adding entry to nil manifest")
	}
}

func TestContextManifest_CheckStaleOnNil(t *testing.T) {
	var m *ContextManifest
	err := m.CheckStale(t.TempDir())
	if err == nil {
		t.Error("expected error checking stale on nil manifest")
	}
}

func TestContextManifest_ExpandOnNil(t *testing.T) {
	var m *ContextManifest
	m.Expand(nil) // should not panic
}

// =============================================================================
// Seam discovery tests: the manifest proves discovery of implementation and
// test seams through its categories and explicit reasons.
// =============================================================================

func TestContextManifest_SeamDiscovery_Implementation(t *testing.T) {
	m := NewContextManifest("Discover implementation seams", DefaultContextManifestBudgets())

	dig := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	seams := []struct {
		path   string
		reason string
	}{
		{"internal/payment/processor.go", "Core payment processing logic"},
		{"internal/payment/validator.go", "Payment validation rules"},
		{"internal/payment/currency.go", "Currency conversion"},
		{"internal/payment/gateway.go", "Gateway integration"},
		{"internal/payment/receipt.go", "Receipt generation"},
	}

	for _, s := range seams {
		if err := m.AddEntry(s.path, dig, fakeRevision("01"), ContextCategoryImplementation, s.reason); err != nil {
			t.Fatalf("AddEntry %q: %v", s.path, err)
		}
	}

	if len(m.Entries) != 5 {
		t.Fatalf("expected 5 implementation entries, got %d", len(m.Entries))
	}

	// Verify all entries are implementation category with non-empty reasons.
	for _, e := range m.Entries {
		if e.Category != ContextCategoryImplementation {
			t.Errorf("entry %q has category %q, want implementation", e.Path, e.Category)
		}
		if e.Reason == "" {
			t.Errorf("entry %q has empty reason", e.Path)
		}
	}
}

func TestContextManifest_SeamDiscovery_TestFiles(t *testing.T) {
	m := NewContextManifest("Discover test seams", DefaultContextManifestBudgets())

	dig := "1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"
	dig2 := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"

	// Add implementation entries first.
	if err := m.AddEntry("internal/payment/processor.go", dig2, fakeRevision("01"), ContextCategoryImplementation, "Payment processor"); err != nil {
		t.Fatal(err)
	}
	if err := m.AddEntry("internal/payment/validator.go", dig2, fakeRevision("02"), ContextCategoryImplementation, "Validator"); err != nil {
		t.Fatal(err)
	}

	// Add test entries.
	tests := []struct {
		path   string
		reason string
	}{
		{"internal/payment/processor_test.go", "Processor unit tests covering edge cases"},
		{"internal/payment/validator_test.go", "Validator boundary tests"},
		{"internal/payment/integration_test.go", "End-to-end payment flow tests"},
	}

	for _, s := range tests {
		if err := m.AddEntry(s.path, dig, fakeRevision("03"), ContextCategoryTest, s.reason); err != nil {
			t.Fatalf("AddEntry %q: %v", s.path, err)
		}
	}

	// Count test entries.
	testCount := 0
	for _, e := range m.Entries {
		if e.Category == ContextCategoryTest {
			testCount++
			if e.Reason == "" {
				t.Errorf("test entry %q has empty reason", e.Path)
			}
		}
	}
	if testCount != 3 {
		t.Errorf("expected 3 test entries, got %d", testCount)
	}
}

func TestContextManifest_SeamDiscovery_Architecture(t *testing.T) {
	m := NewContextManifest("Discover architecture seams", DefaultContextManifestBudgets())

	dig := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	archSeams := []struct {
		path   string
		reason string
	}{
		{"docs/architecture.md", "System architecture overview"},
		{"docs/adr/0001-core-boundaries.md", "ADR: Core domain boundaries"},
	}

	for _, s := range archSeams {
		if err := m.AddEntry(s.path, dig, fakeRevision("01"), ContextCategoryArchitecture, s.reason); err != nil {
			t.Fatalf("AddEntry %q: %v", s.path, err)
		}
	}

	if len(m.Entries) != 2 {
		t.Fatalf("expected 2 architecture entries, got %d", len(m.Entries))
	}
	for _, e := range m.Entries {
		if e.Category != ContextCategoryArchitecture {
			t.Errorf("entry %q has category %q, want architecture", e.Path, e.Category)
		}
	}
}

// =============================================================================
// Bounded output tests: the manifest proves that budgets constrain total
// output to a bounded, predictable size.
// =============================================================================

func TestContextManifest_BoundedOutput_DefaultBudgets(t *testing.T) {
	m := NewContextManifest("Bounded output", DefaultContextManifestBudgets())
	dig := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"

	// Default budgets: 5 impl, 3 test, 2 arch = 10 total.
	totalBudget := m.Budgets.MaxImplementation + m.Budgets.MaxTest + m.Budgets.MaxArchitecture
	if totalBudget != 10 {
		t.Fatalf("default total budget = %d, want 10", totalBudget)
	}

	// Fill to budgets.
	for i := 0; i < m.Budgets.MaxImplementation; i++ {
		path := fmt.Sprintf("impl_%d.go", i)
		if err := m.AddEntry(path, dig, fakeRevision(fmt.Sprintf("%02d", i)), ContextCategoryImplementation, fmt.Sprintf("impl %d", i)); err != nil {
			t.Fatalf("impl entry %d: %v", i, err)
		}
	}
	for i := 0; i < m.Budgets.MaxTest; i++ {
		path := fmt.Sprintf("impl_%d_test.go", i)
		if err := m.AddEntry(path, dig, fakeRevision(fmt.Sprintf("%02d", i+10)), ContextCategoryTest, fmt.Sprintf("test %d", i)); err != nil {
			t.Fatalf("test entry %d: %v", i, err)
		}
	}
	for i := 0; i < m.Budgets.MaxArchitecture; i++ {
		path := fmt.Sprintf("doc_%d.md", i)
		if err := m.AddEntry(path, dig, fakeRevision(fmt.Sprintf("%02d", i+20)), ContextCategoryArchitecture, fmt.Sprintf("doc %d", i)); err != nil {
			t.Fatalf("arch entry %d: %v", i, err)
		}
	}

	if len(m.Entries) != totalBudget {
		t.Errorf("expected %d entries at budget, got %d", totalBudget, len(m.Entries))
	}

	// Any additional entry should fail.
	if err := m.AddEntry("overflow.go", dig, fakeRevision("99"), ContextCategoryImplementation, "overflow"); err == nil {
		t.Error("expected error for overflow entry")
	}
}

// =============================================================================
// Helpers
// =============================================================================

// writeContextManifestRaw writes raw JSON content as the context manifest file,
// bypassing WriteContextManifest's validation. Useful for testing ReadContextManifest.
func writeContextManifestRaw(t *testing.T, dir, content string) {
	t.Helper()
	path := filepath.Join(dir, ContextManifestName)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("writing raw context manifest: %v", err)
	}
}