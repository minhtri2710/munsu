package fleet

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
)

// gitRepo runs git in dir and fails the test on error.
func gitRepo(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// deliveryChangesRepo builds a repo whose origin/main is the base commit, with
// docs/a.md, docs/gone.md, main.go, and internal/cli/skills/s.md at base.
func deliveryChangesRepo(t *testing.T) (dir, base string) {
	t.Helper()
	dir = t.TempDir()
	gitRepo(t, dir, "init", "-q", "-b", "main")
	for p, body := range map[string]string{
		"docs/a.md":                      "a\n",
		"docs/gone.md":                   "gone\n",
		"main.go":                        "package main\n",
		"internal/cli/skills/s/SKILL.md": "skill\n",
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, p), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitRepo(t, dir, "add", "-A")
	gitRepo(t, dir, "commit", "-q", "-m", "base")
	base = gitRepo(t, dir, "rev-parse", "HEAD")
	gitRepo(t, dir, "update-ref", "refs/remotes/origin/main", base)
	origin := t.TempDir()
	gitRepo(t, origin, "init", "-q", "--bare", "-b", "main")
	gitRepo(t, dir, "remote", "add", "origin", origin)
	gitRepo(t, dir, "push", "-q", "origin", "main")
	return dir, base
}

// branchWith checks out a branch from base, applies change, commits, and
// returns the head SHA.
func branchWith(t *testing.T, dir, base string, change func(dir string)) string {
	t.Helper()
	gitRepo(t, dir, "checkout", "-q", "-B", "feature", base)
	change(dir)
	gitRepo(t, dir, "add", "-A")
	gitRepo(t, dir, "commit", "-q", "--allow-empty", "-m", "change")
	return gitRepo(t, dir, "rev-parse", "HEAD")
}

func writeRepoFile(t *testing.T, dir, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, p)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, p), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDeliveryChangesClassifiesTheBaseToHeadRange(t *testing.T) {
	cases := []struct {
		name   string
		change func(dir string)
		want   bool
	}{
		{"modified docs only is prose", func(dir string) { writeRepoFile(t, dir, "docs/a.md", "changed\n") }, true},
		{"added and deleted prose is prose", func(dir string) {
			writeRepoFile(t, dir, "README.md", "new\n")
			gitRepo(t, dir, "rm", "-q", "docs/gone.md")
		}, true},
		{"docs with go source is not prose", func(dir string) {
			writeRepoFile(t, dir, "docs/a.md", "changed\n")
			writeRepoFile(t, dir, "main.go", "package main\n// x\n")
		}, false},
		{"prose moved into code is not prose", func(dir string) {
			gitRepo(t, dir, "mv", "docs/a.md", "a.go")
		}, false},
		{"binary bytes under a prose name are not prose", func(dir string) {
			writeRepoFile(t, dir, "docs/a.md", "bin\x00\x01\x02")
		}, false},
		{"embedded skill edit is not prose", func(dir string) { writeRepoFile(t, dir, "internal/cli/skills/s/SKILL.md", "edited\n") }, false},
		{"empty change is not prose", func(dir string) {}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir, base := deliveryChangesRepo(t)
			head := branchWith(t, dir, base, tc.change)
			ident := domain.DeliveryIdentity{BaseRef: "main", HeadSHA: head}
			if got := deliveryChanges(dir, ident).ProseOnly(); got != tc.want {
				t.Fatalf("deliveryChanges(...).ProseOnly() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDeliveryChangesFailsClosedWhenTheRangeIsUnlisted(t *testing.T) {
	dir, base := deliveryChangesRepo(t)
	head := branchWith(t, dir, base, func(dir string) { writeRepoFile(t, dir, "docs/a.md", "changed\n") })
	cases := []struct {
		name  string
		ident domain.DeliveryIdentity
	}{
		{"unknown base ref", domain.DeliveryIdentity{BaseRef: "nope", HeadSHA: head}},
		{"option-like base ref", domain.DeliveryIdentity{BaseRef: "--output=/tmp/x", HeadSHA: head}},
		{"unknown head", domain.DeliveryIdentity{BaseRef: "main", HeadSHA: "0000000000000000000000000000000000000001"}},
		{"option-like head", domain.DeliveryIdentity{BaseRef: "main", HeadSHA: "--all"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := deliveryChanges(dir, tc.ident); got.Listed || got.ProseOnly() {
				t.Fatalf("deliveryChanges(...) = %+v, want unlisted and not prose", got)
			}
		})
	}
}

func TestDeliveryChangesIgnoresAForgedLocalTrackingRef(t *testing.T) {
	dir, base := deliveryChangesRepo(t)
	withCode := branchWith(t, dir, base, func(dir string) { writeRepoFile(t, dir, "main.go", "package main\n// forged\n") })
	head := branchWith(t, dir, withCode, func(dir string) { writeRepoFile(t, dir, "docs/a.md", "changed\n") })
	gitRepo(t, dir, "update-ref", "refs/remotes/origin/main", withCode)
	ident := domain.DeliveryIdentity{BaseRef: "main", HeadSHA: head}
	if got := deliveryChanges(dir, ident); got.ProseOnly() {
		t.Fatalf("deliveryChanges(...) = %+v, want the code change from the remote base, not prose", got)
	}
}
