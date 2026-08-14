package bootstrap

import (
	"os"
	"strings"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// IsBoundRepository reports whether commonDir is the git common-dir of the
// repository the active task run is bound to.
//
// This is the single owner of the question "is this primary checkout the
// shared checkout munsu is protecting?". `fleet.Identity == Primary` alone does
// not answer it: primary means `gitDir == commonDir`, which is true of every
// clone and every `git init` on the machine — including a reference repo an
// agent clones inside its own worktree, or a throwaway fixture repo it creates
// to reproduce a bug. Refusing those is a false positive with no upside.
//
// The comparison is the same one validateGitTargetBinding
// (internal/cli/git_worktree_safety.go) already uses for git mutations, so the
// two enforcement paths agree on what "the bound repository" means.
//
// Every failure to establish the binding returns false — fail-open. A run whose
// binding cannot be read is not proof that the target is shared state, and the
// cost of guessing wrong here is a stalled agent run.
func IsBoundRepository(commonDir string) bool {
	if commonDir == "" {
		return false
	}
	homeDir := strings.TrimSpace(os.Getenv("MUNSU_HOME"))
	taskID := strings.TrimSpace(os.Getenv("MUNSU_TASK_ID"))
	if homeDir == "" || taskID == "" {
		return false
	}
	h, err := home.Open(homeDir)
	if err != nil {
		return false
	}
	auth, err := taskauthority.NewCanonical(h)
	if err != nil {
		return false
	}
	tid, err := domain.NewTaskID(taskID)
	if err != nil {
		return false
	}
	agg, err := auth.Get(tid)
	if err != nil || agg.Worktree == nil {
		return false
	}
	return agg.Worktree.CommonDir == commonDir
}
