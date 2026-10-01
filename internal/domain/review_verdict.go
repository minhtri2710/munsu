package domain

import (
	"fmt"
	"strings"
)

// VerdictOutcome is the closed set of results a review verdict records. Only
// VerdictPass approves delivery.
type VerdictOutcome string

const (
	VerdictPass VerdictOutcome = "pass"
	VerdictFail VerdictOutcome = "fail"
)

// Valid reports whether the outcome is a known verdict outcome.
func (o VerdictOutcome) Valid() bool { return o == VerdictPass || o == VerdictFail }

// TreeState is the reviewed checkout observed at one instant: its HEAD and
// the sha256 digest of its `git status --porcelain` output.
type TreeState struct {
	Head      string `json:"head"`
	Porcelain string `json:"porcelain"`
}

// ReviewVerdict is one reviewer's judgment of one exact head. It is the only
// approval source for delivery (ADR-0025): a provider review state never
// approves. HeadSHA is the reviewed head and BaseSHA the reviewed range base.
// ReviewerTask and ReviewerGeneration name the munsu review task generation
// that produced the verdict and ReviewerIncarnation its endpoint instance;
// Author is the authoring soldier instance the reviewer must differ from.
// Before and After are the review-custody observations of the checkout (G2):
// the review is valid only if HEAD and porcelain were equal before and after
// it.
//
// The reviewer fields are taken from the review task's aggregate by the record
// step, never from the verdict file; they are consistency facts, not
// authentication (ADR-0025 Decision 3).
type ReviewVerdict struct {
	Outcome             VerdictOutcome `json:"outcome"`
	HeadSHA             string         `json:"head_sha"`
	BaseSHA             string         `json:"base_sha"`
	ReviewerTask        string         `json:"reviewer_task"`
	ReviewerGeneration  uint64         `json:"reviewer_generation"`
	ReviewerIncarnation string         `json:"reviewer_incarnation"`
	Author              string         `json:"author"`
	Before              TreeState      `json:"before"`
	After               TreeState      `json:"after"`
}

// Validate checks the verdict is well formed and its review was legitimate: a
// known outcome, every identity present, a non-empty review range, a reviewer
// instance that is not the author, and a frozen tree whose recorded HEAD is the
// reviewed head and did not move.
func (v ReviewVerdict) Validate() error {
	switch {
	case !v.Outcome.Valid():
		return fmt.Errorf("review verdict: invalid outcome %q", v.Outcome)
	case strings.TrimSpace(v.HeadSHA) == "":
		return fmt.Errorf("review verdict: head SHA is required")
	case strings.TrimSpace(v.BaseSHA) == "":
		return fmt.Errorf("review verdict: base SHA is required")
	case v.BaseSHA == v.HeadSHA:
		return fmt.Errorf("review verdict: review range base equals head %s", v.HeadSHA)
	case strings.TrimSpace(v.ReviewerTask) == "":
		return fmt.Errorf("review verdict: reviewer task is required")
	case v.ReviewerGeneration == 0:
		return fmt.Errorf("review verdict: reviewer generation is required")
	case strings.TrimSpace(v.ReviewerIncarnation) == "":
		return fmt.Errorf("review verdict: reviewer incarnation is required")
	case strings.TrimSpace(v.Author) == "":
		return fmt.Errorf("review verdict: author is required")
	case v.ReviewerIncarnation == v.Author:
		return fmt.Errorf("review verdict: reviewer instance %q is the authoring soldier instance", v.ReviewerIncarnation)
	case !IsSHA256(v.Before.Porcelain) || !IsSHA256(v.After.Porcelain):
		return fmt.Errorf("review verdict: porcelain state must be a sha256 digest")
	case v.Before.Head != v.HeadSHA:
		return fmt.Errorf("review verdict: reviewed tree head %q is not the reviewed head %q", v.Before.Head, v.HeadSHA)
	case v.Before != v.After:
		return fmt.Errorf("review verdict: the reviewed tree moved during the review")
	}
	return nil
}

// Approves reports why the verdict does not approve delivering headSHA of
// reviewedTask by the soldier instance author, or nil when it does: the
// verdict must be valid, PASS, bound to exactly headSHA, record exactly author
// as the authoring instance, and come from a review task other than the
// reviewed task itself.
func (v ReviewVerdict) Approves(headSHA, author, reviewedTask string) error {
	if err := v.Validate(); err != nil {
		return err
	}
	switch {
	case v.ReviewerTask == reviewedTask:
		return fmt.Errorf("review verdict: task %q cannot review itself", reviewedTask)
	case v.Outcome != VerdictPass:
		return fmt.Errorf("review verdict is %q, not %q", v.Outcome, VerdictPass)
	case v.HeadSHA != headSHA:
		return fmt.Errorf("review verdict is bound to head %q, not %q", v.HeadSHA, headSHA)
	case v.Author != author:
		return fmt.Errorf("review verdict names author %q, not the authoring soldier instance %q", v.Author, author)
	}
	return nil
}
