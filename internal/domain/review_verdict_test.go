package domain_test

import (
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
)

const (
	verdictHead   = "1111111111111111111111111111111111111111"
	verdictBase   = "2222222222222222222222222222222222222222"
	verdictTree   = "3333333333333333333333333333333333333333333333333333333333333333"
	verdictAuthor = "soldier-incarnation-1"
)

func passVerdict() domain.ReviewVerdict {
	tree := domain.TreeState{Head: verdictHead, Porcelain: verdictTree}
	return domain.ReviewVerdict{
		Outcome: domain.VerdictPass, HeadSHA: verdictHead, BaseSHA: verdictBase,
		ReviewerTask: "review-1", ReviewerGeneration: 1, ReviewerIncarnation: "reviewer-incarnation-1",
		Author: verdictAuthor, Before: tree, After: tree,
	}
}

func TestReviewVerdictValidateRefusesAnIllegitimateReview(t *testing.T) {
	if err := passVerdict().Validate(); err != nil {
		t.Fatalf("fixture verdict is refused, so no case below can attribute its refusal: %v", err)
	}
	moved := domain.TreeState{Head: verdictHead, Porcelain: strings.Repeat("4", 64)}
	cases := []struct {
		name   string
		mutate func(*domain.ReviewVerdict)
		want   string
	}{
		{"unknown outcome", func(v *domain.ReviewVerdict) { v.Outcome = "maybe" }, "invalid outcome"},
		{"no head", func(v *domain.ReviewVerdict) { v.HeadSHA = " " }, "head SHA is required"},
		{"no base", func(v *domain.ReviewVerdict) { v.BaseSHA = "" }, "base SHA is required"},
		{"empty review range", func(v *domain.ReviewVerdict) { v.BaseSHA = v.HeadSHA }, "base equals head"},
		{"no reviewer task", func(v *domain.ReviewVerdict) { v.ReviewerTask = "" }, "reviewer task is required"},
		{"no reviewer generation", func(v *domain.ReviewVerdict) { v.ReviewerGeneration = 0 }, "reviewer generation is required"},
		{"no reviewer incarnation", func(v *domain.ReviewVerdict) { v.ReviewerIncarnation = "" }, "reviewer incarnation is required"},
		{"no author", func(v *domain.ReviewVerdict) { v.Author = "" }, "author is required"},
		{"reviewer is the author", func(v *domain.ReviewVerdict) { v.ReviewerIncarnation = v.Author }, "is the authoring soldier instance"},
		{"before porcelain is not a digest", func(v *domain.ReviewVerdict) { v.Before.Porcelain = "dirty" }, "porcelain state must be a sha256 digest"},
		{"after porcelain is not a digest", func(v *domain.ReviewVerdict) { v.After.Porcelain = "" }, "porcelain state must be a sha256 digest"},
		{"tree head is not the reviewed head", func(v *domain.ReviewVerdict) { v.Before.Head = verdictBase; v.After.Head = verdictBase }, "is not the reviewed head"},
		{"tree moved during the review", func(v *domain.ReviewVerdict) { v.After = moved }, "tree moved during the review"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := passVerdict()
			tc.mutate(&v)
			err := v.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate() = %v, want a refusal containing %q", err, tc.want)
			}
		})
	}
}

func TestReviewVerdictApprovesBindsHeadAuthorAndReviewedTask(t *testing.T) {
	if err := passVerdict().Approves(verdictHead, verdictAuthor, "ship-1"); err != nil {
		t.Fatalf("Approves on the matching head, author and task = %v, want nil", err)
	}
	cases := []struct {
		name     string
		mutate   func(*domain.ReviewVerdict)
		head     string
		author   string
		reviewed string
		want     string
	}{
		{"invalid verdict", func(v *domain.ReviewVerdict) { v.BaseSHA = "" }, verdictHead, verdictAuthor, "ship-1", "base SHA is required"},
		{"task reviews itself", func(v *domain.ReviewVerdict) {}, verdictHead, verdictAuthor, "review-1", "cannot review itself"},
		{"fail outcome", func(v *domain.ReviewVerdict) { v.Outcome = domain.VerdictFail }, verdictHead, verdictAuthor, "ship-1", `is "fail"`},
		{"another head", func(v *domain.ReviewVerdict) {}, verdictBase, verdictAuthor, "ship-1", "is bound to head"},
		{"another author", func(v *domain.ReviewVerdict) {}, verdictHead, "soldier-incarnation-2", "ship-1", "names author"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := passVerdict()
			tc.mutate(&v)
			err := v.Approves(tc.head, tc.author, tc.reviewed)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Approves() = %v, want a refusal containing %q", err, tc.want)
			}
		})
	}
}

func TestWordsValidateRefusesEachMissingField(t *testing.T) {
	full := domain.Words{Grantor: "beo", Channel: "supervisor-relay", Quote: "munsu: apply"}
	if err := full.Validate(); err != nil {
		t.Fatalf("complete words refused: %v", err)
	}
	cases := []struct {
		name   string
		mutate func(*domain.Words)
		want   string
	}{
		{"no grantor", func(w *domain.Words) { w.Grantor = " " }, "grantor is required"},
		{"no channel", func(w *domain.Words) { w.Channel = "" }, "channel is required"},
		{"empty quote", func(w *domain.Words) { w.Quote = "\t" }, "quote is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := full
			tc.mutate(&w)
			if err := w.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate() = %v, want a refusal containing %q", err, tc.want)
			}
		})
	}
}
