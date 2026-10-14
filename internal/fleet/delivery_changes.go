package fleet

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/domain"
)

// deliveryChanges lists what a delivery changes between the merge base of its
// base branch and its head. The base is origin's branch tip, fetched into a
// private ref so a stale or forged refs/remotes/origin/* never decides it. Any
// git failure yields an unlisted diff, which requires a verdict; the failure is
// never surfaced as prose.
func deliveryChanges(worktreePath string, ident domain.DeliveryIdentity) domain.DeliveryDiff {
	if ident.BaseRef == "" || strings.HasPrefix(ident.BaseRef, "-") || ident.HeadSHA == "" || strings.HasPrefix(ident.HeadSHA, "-") {
		return domain.DeliveryDiff{}
	}
	baseRef := fmt.Sprintf("refs/munsu/delivery-base/%d-%d", os.Getpid(), time.Now().UnixNano())
	if _, err := gitOutput(worktreePath, "fetch", "--quiet", "--no-tags", "--no-write-fetch-head", "--refmap=", "origin", "+refs/heads/"+ident.BaseRef+":"+baseRef); err != nil {
		return domain.DeliveryDiff{}
	}
	defer gitOutput(worktreePath, "update-ref", "-d", baseRef)
	base, err := gitOutput(worktreePath, "merge-base", baseRef, ident.HeadSHA)
	if err != nil || base == "" {
		return domain.DeliveryDiff{}
	}
	raw, err := gitOutput(worktreePath, "diff", "--no-renames", "--raw", "-z", base, ident.HeadSHA)
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
	tokens := strings.Split(raw, "\x00")
	diff := domain.DeliveryDiff{Listed: true}
	for i := 0; i+1 < len(tokens); i += 2 {
		meta := strings.Fields(strings.TrimPrefix(tokens[i], ":"))
		if len(meta) != 5 {
			return domain.DeliveryDiff{}
		}
		p := tokens[i+1]
		typed := !regularMode(meta[0]) || !regularMode(meta[1])
		diff.Changes = append(diff.Changes, domain.DeliveryChange{Path: p, Status: meta[4], Binary: binary[p], Typed: typed})
	}
	if len(diff.Changes) == 0 {
		return domain.DeliveryDiff{}
	}
	return diff
}

// regularMode is a plain file mode, or 000000 for the absent side of an add or
// delete. Anything else (symlink 120000, submodule 160000) is typed.
func regularMode(mode string) bool {
	return mode == "100644" || mode == "100755" || mode == "000000"
}

func gitOutput(worktreePath string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", worktreePath}, args...)...).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(out), "\x00\n"), nil
}
