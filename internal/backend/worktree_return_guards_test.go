package backend

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/testutil"
)

// cleanReturnWorktree is a detached git worktree of a one-commit repo, with
// nothing untracked or ignored in it.
func cleanReturnWorktree(t *testing.T) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "repo")
	defaultBranchGit(t, "", "git", "init", repo)
	defaultBranchGit(t, repo, "git", "config", "user.email", "test@test.com")
	defaultBranchGit(t, repo, "git", "config", "user.name", "Test")
	for name, content := range map[string]string{"tracked": "clean\n", ".gitignore": "ignored-data.txt\n"} {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	defaultBranchGit(t, repo, "git", "add", ".")
	defaultBranchGit(t, repo, "git", "commit", "-m", "initial")
	worktree := filepath.Join(t.TempDir(), "worktree")
	defaultBranchGit(t, repo, "git", "worktree", "add", "--detach", worktree, "HEAD")
	return worktree
}

// TestWorktreeGuards is the one table for the worktree provider refusals:
// Return's shared assertReturnable owner on both providers, and the treehouse
// recovery fence. Each refusal has a blocking row and its nearest allowing row.
func TestWorktreeGuards(t *testing.T) {
	// A refused Return must touch neither the worktree nor the provider; an
	// allowed one reaches it.
	type provider struct {
		name  string
		verb  string
		setup func(t *testing.T) (ret func(path string) error, reached func(path string) bool)
	}
	providers := []provider{
		{"gitWorktree", "remove", func(t *testing.T) (func(string) error, func(string) bool) {
			p := &gitWorktreeProvider{homeDir: t.TempDir()}
			return p.Return, func(path string) bool { _, err := os.Stat(path); return filepath.IsAbs(path) && os.IsNotExist(err) }
		}},
		{"treehouse", "return", func(t *testing.T) (func(string) error, func(string) bool) {
			trace := filepath.Join(t.TempDir(), "treehouse-invoked")
			testutil.FakeOnPath(t, "treehouse", fmt.Sprintf("#!/bin/sh\n: > %q\nexit 0\n", trace))
			return (&treehouseProvider{}).Return, func(string) bool { _, err := os.Stat(trace); return err == nil }
		}},
	}
	type returnCase struct {
		name    string
		prepare func(t *testing.T) (path string, survives map[string]string)
		want    string // "" allows the return
	}
	cases := []returnCase{
		{"relative path", func(t *testing.T) (string, map[string]string) { return "relative/worktree", nil },
			"refusing to %s worktree with a non-absolute path"},
		{"directory .git marker", func(t *testing.T) (string, map[string]string) {
			worktree := t.TempDir()
			if err := os.Mkdir(filepath.Join(worktree, ".git"), 0o755); err != nil {
				t.Fatal(err)
			}
			sentinel := filepath.Join(worktree, "user-data.txt")
			if err := os.WriteFile(sentinel, []byte("must survive\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			return worktree, map[string]string{sentinel: "must survive\n"}
		}, "refusing to %s worktree with a non-regular .git marker"},
		{"marker without gitdir pointer", func(t *testing.T) (string, map[string]string) {
			worktree := t.TempDir()
			if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("not a gitdir pointer\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			return worktree, nil
		}, "unexpected .git file format: not a gitdir pointer"},
		{"dirty, untracked and ignored content", func(t *testing.T) (string, map[string]string) {
			worktree := cleanReturnWorktree(t)
			survives := map[string]string{
				filepath.Join(worktree, "tracked"):          "modified\n",
				filepath.Join(worktree, "user-data.txt"):    "must survive\n",
				filepath.Join(worktree, "ignored-data.txt"): "also survives\n",
			}
			for path, content := range survives {
				if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			return worktree, survives
		}, "refusing to %s worktree with dirty, untracked, or ignored content"},
		{"clean worktree", func(t *testing.T) (string, map[string]string) { return cleanReturnWorktree(t), nil }, ""},
	}
	for _, p := range providers {
		for _, tc := range cases {
			t.Run(p.name+"/"+tc.name, func(t *testing.T) {
				ret, reached := p.setup(t)
				path, survives := tc.prepare(t)
				err := ret(path)
				if tc.want == "" {
					if err != nil || !reached(path) {
						t.Fatalf("Return = %v, reached = %v; want a clean worktree released", err, reached(path))
					}
					return
				}
				if want := strings.ReplaceAll(tc.want, "%s", p.verb); err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("Return error = %v, want %q", err, want)
				}
				if reached(path) {
					t.Fatal("provider reached on a refused worktree")
				}
				for file, want := range survives {
					if got, err := os.ReadFile(file); err != nil || string(got) != want {
						t.Errorf("%s = %q, err=%v; refused return must not remove it", filepath.Base(file), got, err)
					}
				}
			})
		}
	}

	for _, tc := range []struct {
		name, holders, want string // want: the acquired path, or empty for the refusal
	}{
		{"treehouse/recovery of a held reservation", `[{"path":"/pool/wt","lease_holder":"reservation"}]`, ""},
		{"treehouse/recovery of a reservation nothing holds", `[{"path":"/pool/other","lease_holder":"other"}]`, "/pool/wt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testutil.FakeOnPath(t, "treehouse", "#!/bin/sh\nif [ \"$1\" = status ]; then printf '%s\\n' '"+tc.holders+"'; exit 0; fi\nprintf '%s\\n' /pool/wt\n")
			got, err := (&treehouseProvider{}).GetReserved(t.TempDir(), false, "reservation", true)
			if tc.want == "" {
				if !errors.Is(err, ErrWorktreeReservationRecoveryUnsupported) || got != "" {
					t.Fatalf("GetReserved = %q, %v; want unsupported-recovery refusal", got, err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("GetReserved = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
	t.Run("treehouse/first acquisition", func(t *testing.T) {
		testutil.FakeOnPath(t, "treehouse", "#!/bin/sh\nprintf '%s\\n' /pool/wt\nexit 0\n")
		got, err := (&treehouseProvider{}).GetReserved(t.TempDir(), false, "reservation", false)
		if err != nil || got != "/pool/wt" {
			t.Fatalf("GetReserved = %q, %v; want the pool worktree", got, err)
		}
	})
}
