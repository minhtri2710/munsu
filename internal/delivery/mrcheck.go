package delivery

import (
	"fmt"

	"github.com/minhtri2710/munsu/internal/task"
)

// MRLiveCheck runs the GitLab MR equivalent of PRCheck.
// It captures the full delivery identity (MR URL, head SHA, source/target branch,
// owner, repo, provider, and timestamp) in task meta using the typed GitLabClient.
// Unlike PRCheck, it does not write a bare-glab polling script — the watcher
// polls provider status through the typed authority seam after terminal
// MR notification.
func MRLiveCheck(homeDir string, id, mrURL string) error {
	// Capture the full delivery identity atomically via typed GitLabClient
	ident, err := CaptureIdentity(mrURL)
	if err != nil {
		return fmt.Errorf("capturing delivery identity: %w", err)
	}

	// Validate identity before persisting
	if err := ValidateIdentity(ident); err != nil {
		return fmt.Errorf("captured identity is incomplete: %w", err)
	}

	// Read existing meta (ignore error if it doesn't exist)
	meta, err := task.ReadMeta(homeDir, id)
	if err != nil {
		meta = make(map[string]string)
	}

	// Write full delivery identity to meta
	for k, v := range ident.ToMeta() {
		meta[k] = v
	}
	meta["pr"] = mrURL              // legacy key for compatibility
	meta["pr_head"] = ident.HeadSHA // legacy key for compatibility

	if err := task.WriteMeta(homeDir, id, meta); err != nil {
		return fmt.Errorf("writing task meta: %w", err)
	}

	fmt.Printf("MR check armed for %s/%s!%d\n", ident.Owner, ident.Repo, ident.Number)
	fmt.Printf("  MR URL:   %s\n", mrURL)
	fmt.Printf("  Head SHA: %s\n", ident.HeadSHA)
	fmt.Printf("  Provider: %s\n", ident.Provider)
	return nil
}
