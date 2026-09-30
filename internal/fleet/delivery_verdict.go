package fleet

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// This file is the Fleet side of the review verdict (ADR-0025): it reads the
// verdict file a munsu review task wrote, observes the reviewed worktree for
// the review-custody record, and records the verdict through
// Canonical.RecordReviewVerdict, the only writer of the approval source
// Deliver's authorization requires.

const (
	reviewVerdictFileName = "verdict.json"
	reviewVerdictSchema   = 1
	// reviewVerdictMaxBytes bounds the verdict file a reviewer can make the
	// record step read.
	reviewVerdictMaxBytes = 256 << 10
)

// reviewVerdictFileDoc is the verdict file: the reviewer's judgment and its
// claimed head and base, plus the identity it believes it has. Identity is
// never taken from it; every identity field is checked against the review
// task's definition and aggregate.
type reviewVerdictFileDoc struct {
	SchemaVersion int                   `json:"schema_version"`
	Task          string                `json:"task"`
	Generation    uint64                `json:"generation"`
	Reviews       string                `json:"reviews"`
	Outcome       domain.VerdictOutcome `json:"outcome"`
	HeadSHA       string                `json:"head_sha"`
	BaseSHA       string                `json:"base_sha"`
	Evidence      string                `json:"evidence"`
}

// ReviewTaskRef names one working review task generation.
type ReviewTaskRef struct {
	TaskID     string
	Generation uint64
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

func openVerdictCanonical(homeDir string) (*taskauthority.Canonical, error) {
	h, err := home.Open(homeDir)
	if err != nil {
		return nil, fmt.Errorf("review verdict: opening home: %w", err)
	}
	c, err := taskauthority.NewCanonical(h)
	if err != nil {
		return nil, fmt.Errorf("review verdict: composing task authority: %w", err)
	}
	return c, nil
}

func resolveVerdictTask(homeDir, taskID string) (*taskauthority.Canonical, taskauthority.Aggregate, error) {
	c, err := openVerdictCanonical(homeDir)
	if err != nil {
		return nil, taskauthority.Aggregate{}, err
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

// WorkingReviewTasks lists the current working review tasks that have an
// endpoint bound: the tasks whose verdict file the supervision watcher waits
// for.
func WorkingReviewTasks(homeDir string) ([]ReviewTaskRef, error) {
	c, err := openVerdictCanonical(homeDir)
	if err != nil {
		return nil, err
	}
	aggs, err := c.List()
	if err != nil {
		return nil, fmt.Errorf("review verdict: listing tasks: %w", err)
	}
	var refs []ReviewTaskRef
	for _, agg := range aggs {
		if agg.Definition.Kind == taskauthority.KindReview && agg.Phase == taskauthority.PhaseWorking && agg.Endpoint != nil {
			refs = append(refs, ReviewTaskRef{TaskID: agg.TaskID, Generation: uint64(agg.Generation)})
		}
	}
	return refs, nil
}

// readReviewVerdictFile reads the review generation's verdict file. It reports
// absent for a missing file and refuses anything but a bounded regular file,
// so a symlink or a device the reviewer left there is read as nothing.
func readReviewVerdictFile(homeDir, taskID string, generation uint64) (data []byte, present bool, err error) {
	path := reviewVerdictFile(reviewLaunchDir(homeDir, taskID, fmt.Sprint(generation)))
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, true, fmt.Errorf("reading verdict file: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > reviewVerdictMaxBytes {
		return nil, true, fmt.Errorf("verdict file %s is not a regular file of at most %d bytes", path, reviewVerdictMaxBytes)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		return nil, true, fmt.Errorf("reading verdict file: %w", err)
	}
	return data, true, nil
}

func verdictFileDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// ObserveReviewVerdictFile is the resolver of the review-verdict process event:
// it reports whether the review generation's verdict file exists and returns its
// sha256 digest (empty when it exists but cannot be read; the record step then
// reports why). It writes nothing.
func ObserveReviewVerdictFile(homeDir, taskID string, generation uint64) (bool, []byte, error) {
	data, present, err := readReviewVerdictFile(homeDir, taskID, generation)
	if !present {
		return false, nil, nil
	}
	if err != nil {
		return true, nil, nil
	}
	return true, []byte(verdictFileDigest(data)), nil
}

// decodeReviewVerdictFile strictly decodes the verdict file: one JSON object,
// no unknown field, nothing after it.
func decodeReviewVerdictFile(data []byte) (reviewVerdictFileDoc, error) {
	var doc reviewVerdictFileDoc
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return reviewVerdictFileDoc{}, fmt.Errorf("verdict file is malformed: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return reviewVerdictFileDoc{}, fmt.Errorf("verdict file is malformed: data after the verdict object")
	}
	return doc, nil
}

// RecordReviewVerdict is the one record step of a review verdict, run by the
// supervision watcher's review-verdict arm and by `delivery record-verdict
// --reviewer-task`. It reads the review task's verdict file, checks that it
// speaks for exactly this task, generation and reviewed head, builds the
// verdict from the review task's definition and aggregate (never from the
// file), observes the reviewed worktree after the review, and records the
// verdict on the reviewed task. generation and wantDigest pin the review
// generation and the file content the caller observed; zero and empty mean the
// current generation and whatever file is there. It returns a one-line summary
// of what it recorded.
func RecordReviewVerdict(homeDir, reviewTaskID string, generation uint64, wantDigest string) (string, error) {
	c, err := openVerdictCanonical(homeDir)
	if err != nil {
		return "", err
	}
	rid, err := domain.NewTaskID(reviewTaskID)
	if err != nil {
		return "", err
	}
	rev, err := c.Get(rid)
	if err != nil {
		return "", fmt.Errorf("review verdict %s: resolving review task: %w", reviewTaskID, err)
	}
	if rev.Definition.Kind != taskauthority.KindReview {
		return "", fmt.Errorf("review verdict: task %s is a %s task, not a review task", reviewTaskID, rev.Definition.Kind)
	}
	if generation != 0 && uint64(rev.Generation) != generation {
		return "", fmt.Errorf("review verdict: task %s is at generation %s, not the observed generation %d", reviewTaskID, rev.Generation, generation)
	}
	if rev.Endpoint == nil || rev.LaunchEvidence == nil || rev.LaunchEvidence.ReviewTree == nil {
		return "", fmt.Errorf("review verdict: review task %s has no bound endpoint or recorded tree before its review", reviewTaskID)
	}
	data, present, err := readReviewVerdictFile(homeDir, reviewTaskID, uint64(rev.Generation))
	if !present {
		return "", fmt.Errorf("review verdict: task %s generation %s wrote no verdict file", reviewTaskID, rev.Generation)
	}
	if err != nil {
		return "", err
	}
	if wantDigest != "" && verdictFileDigest(data) != wantDigest {
		return "", fmt.Errorf("review verdict: the verdict file of %s changed after it was observed", reviewTaskID)
	}
	doc, err := decodeReviewVerdictFile(data)
	if err != nil {
		return "", err
	}
	switch {
	case doc.SchemaVersion != reviewVerdictSchema:
		return "", fmt.Errorf("review verdict: verdict file schema_version %d is not %d", doc.SchemaVersion, reviewVerdictSchema)
	case doc.Task != reviewTaskID:
		return "", fmt.Errorf("review verdict: verdict file is for task %q, not %s", doc.Task, reviewTaskID)
	case doc.Generation != uint64(rev.Generation):
		return "", fmt.Errorf("review verdict: verdict file is for generation %d, not %s", doc.Generation, rev.Generation)
	case doc.Reviews != rev.Definition.ReviewTaskID:
		return "", fmt.Errorf("review verdict: verdict file reviews %q, not %s", doc.Reviews, rev.Definition.ReviewTaskID)
	case doc.HeadSHA != rev.Definition.ReviewHead:
		return "", fmt.Errorf("review verdict: verdict file is for head %q, not the reviewed head %q", doc.HeadSHA, rev.Definition.ReviewHead)
	case strings.TrimSpace(doc.Evidence) == "":
		return "", fmt.Errorf("review verdict: verdict file carries no evidence")
	}
	shipID, err := domain.NewTaskID(rev.Definition.ReviewTaskID)
	if err != nil {
		return "", err
	}
	ship, err := c.Get(shipID)
	if err != nil {
		return "", fmt.Errorf("review verdict: resolving reviewed task %s: %w", shipID, err)
	}
	if ship.Worktree == nil || ship.Endpoint == nil {
		return "", fmt.Errorf("review verdict requires the bound worktree and endpoint of task %s", shipID)
	}
	after, err := observeTree(ship.Worktree.Path)
	if err != nil {
		return "", err
	}
	creq := taskauthority.CanonicalRecordReviewVerdictRequest{
		HomeID:       c.HomeID(),
		TaskID:       shipID,
		Precondition: domain.Of(uint64(ship.Generation), uint64(ship.Revision)),
		Verdict: domain.ReviewVerdict{
			Outcome:             doc.Outcome,
			HeadSHA:             doc.HeadSHA,
			BaseSHA:             doc.BaseSHA,
			ReviewerTask:        reviewTaskID,
			ReviewerGeneration:  uint64(rev.Generation),
			ReviewerIncarnation: rev.Endpoint.Incarnation,
			Author:              ship.Endpoint.Incarnation,
			Before:              *rev.LaunchEvidence.ReviewTree,
			After:               after,
		},
	}
	op := deliveryJournals.mustOperation("verdict-"+newJournalID()+"-"+shipID.Value(), creq)
	if _, err := c.RecordReviewVerdict(op, creq); err != nil {
		return "", fmt.Errorf("recording review verdict: %w", err)
	}
	return fmt.Sprintf("review verdict %s for task %s at head %s recorded from %s generation %s", doc.Outcome, shipID, doc.HeadSHA, reviewTaskID, rev.Generation), nil
}
