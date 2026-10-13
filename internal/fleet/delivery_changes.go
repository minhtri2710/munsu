package fleet

import (
	"os/exec"
	"strings"

	"github.com/minhtri2710/munsu/internal/domain"
)

// deliveryChanges lists what a delivery changes between the merge base of its
// base branch and its head. The base is origin/<BaseRef> as the worktree knows
// it, matching the provider merge target. Any git failure yields an unlisted
// diff, which requires a verdict; the failure is never surfaced as prose.
func deliveryChanges(worktreePath string, ident domain.DeliveryIdentity) domain.DeliveryDiff {
	if ident.BaseRef == "" || strings.HasPrefix(ident.BaseRef, "-") || ident.HeadSHA == "" || strings.HasPrefix(ident.HeadSHA, "-") {
		return domain.DeliveryDiff{}
	}
	base, err := gitOutput(worktreePath, "merge-base", "origin/"+ident.BaseRef, ident.HeadSHA)
	if err != nil || base == "" {
		return domain.DeliveryDiff{}
	}
	names, err := gitOutput(worktreePath, "diff", "--no-renames", "--name-status", "-z", base, ident.HeadSHA)
	if err != nil {
		return domain.DeliveryDiff{}
	}
	numstat, err := gitOutput(worktreePath, "diff", "--no-renames", "--numstat", "-z", base, ident.HeadSHA)
	if err != nil {
		return domain.DeliveryDiff{}
	}
	binary := map[string]bool{}
	for _, rec := range strings.Split(numstat, "\x00") {
		fields := strings.SplitN(rec, "\t", 3)
		if len(fields) == 3 && fields[0] == "-" && fields[1] == "-" {
			binary[fields[2]] = true
		}
	}
	tokens := strings.Split(names, "\x00")
	diff := domain.DeliveryDiff{Listed: true}
	for i := 0; i+1 < len(tokens); i += 2 {
		p := tokens[i+1]
		diff.Changes = append(diff.Changes, domain.DeliveryChange{Path: p, Status: tokens[i], Binary: binary[p]})
	}
	if len(diff.Changes) == 0 {
		return domain.DeliveryDiff{}
	}
	return diff
}

func gitOutput(worktreePath string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", worktreePath}, args...)...).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(out), "\x00\n"), nil
}
