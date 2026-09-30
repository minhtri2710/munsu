package fleet

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os/exec"
	"strings"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// This file is the Fleet side of the review verdict (ADR-0025): it observes
// the bound worktree for the review-custody record and records the reviewer's
// verdict through Canonical.RecordReviewVerdict, the only writer of the
// approval source Deliver's authorization requires.

// ReviewVerdictRequest is one reviewer's claim about one exact head. Before is
// the reviewed tree as the reviewer observed it when the review began
// (ObserveReviewTree); the tree after the review is observed here, at record
// time.
type ReviewVerdictRequest struct {
	Outcome  domain.VerdictOutcome
	HeadSHA  string
	BaseSHA  string
	Reviewer string
	Before   domain.TreeState
}

// ObserveReviewTree observes the task's bound worktree: its HEAD and the
// sha256 digest of its `git status --porcelain` output.
func ObserveReviewTree(homeDir, taskID string) (domain.TreeState, error) {
	_, agg, err := resolveVerdictTask(homeDir, taskID)
	if err != nil {
		return domain.TreeState{}, err
	}
	return observeTree(agg.Worktree.Path)
}

func observeTree(worktreePath string) (domain.TreeState, error) {
	head, err := exec.Command("git", "-C", worktreePath, "rev-parse", "HEAD").Output()
	if err != nil {
		return domain.TreeState{}, fmt.Errorf("observing worktree HEAD: %w", err)
	}
	porcelain, err := exec.Command("git", "-C", worktreePath, "status", "--porcelain").Output()
	if err != nil {
		return domain.TreeState{}, fmt.Errorf("observing worktree status: %w", err)
	}
	sum := sha256.Sum256(porcelain)
	return domain.TreeState{Head: strings.TrimSpace(string(head)), Porcelain: hex.EncodeToString(sum[:])}, nil
}

func resolveVerdictTask(homeDir, taskID string) (*taskauthority.Canonical, taskauthority.Aggregate, error) {
	h, err := home.Open(homeDir)
	if err != nil {
		return nil, taskauthority.Aggregate{}, fmt.Errorf("review verdict %s: opening home: %w", taskID, err)
	}
	c, err := taskauthority.NewCanonical(h)
	if err != nil {
		return nil, taskauthority.Aggregate{}, fmt.Errorf("review verdict %s: composing task authority: %w", taskID, err)
	}
	tid, err := domain.NewTaskID(taskID)
	if err != nil {
		return nil, taskauthority.Aggregate{}, err
	}
	agg, err := c.Get(tid)
	if err != nil {
		return nil, taskauthority.Aggregate{}, fmt.Errorf("review verdict %s: resolving task: %w", taskID, err)
	}
	if agg.Worktree == nil || agg.Endpoint == nil {
		return nil, taskauthority.Aggregate{}, fmt.Errorf("review verdict requires the bound worktree and endpoint of task %s", taskID)
	}
	return c, agg, nil
}

// RecordReviewVerdict records the reviewer's verdict for the task's bound
// head. The authoring instance is the bound endpoint's incarnation, and a
// reviewer that is the task's own endpoint (its incarnation or its handle) is
// refused before anything is written. The canonical operation refuses a head
// other than the bound worktree head, a moved tree and every other verdict
// invalidity (domain.ReviewVerdict.Validate).
func RecordReviewVerdict(homeDir, taskID string, req ReviewVerdictRequest) error {
	c, agg, err := resolveVerdictTask(homeDir, taskID)
	if err != nil {
		return err
	}
	if req.Reviewer == agg.Endpoint.Incarnation || req.Reviewer == agg.Endpoint.Handle {
		return fmt.Errorf("review verdict reviewer %q is the endpoint of task %s; a task cannot review itself", req.Reviewer, taskID)
	}
	after, err := observeTree(agg.Worktree.Path)
	if err != nil {
		return err
	}
	tid, err := domain.NewTaskID(taskID)
	if err != nil {
		return err
	}
	creq := taskauthority.CanonicalRecordReviewVerdictRequest{
		HomeID:       c.HomeID(),
		TaskID:       tid,
		Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
		Verdict: domain.ReviewVerdict{
			Outcome:  req.Outcome,
			HeadSHA:  req.HeadSHA,
			BaseSHA:  req.BaseSHA,
			Reviewer: req.Reviewer,
			Author:   agg.Endpoint.Incarnation,
			Before:   req.Before,
			After:    after,
		},
	}
	op := deliveryJournals.mustOperation("verdict-"+newJournalID()+"-"+taskID, creq)
	if _, err := c.RecordReviewVerdict(op, creq); err != nil {
		return fmt.Errorf("recording review verdict: %w", err)
	}
	return nil
}
