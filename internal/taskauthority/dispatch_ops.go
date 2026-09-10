package taskauthority

import (
	"slices"
)

// HoldResult is the outcome of a dispatch-control operation.
type HoldResult struct {
	HoldID   string
	Replayed bool
}

func normalizeScope(scope DispatchHoldScope) DispatchHoldScope {
	return DispatchHoldScope{
		ProjectIDs:  uniqueSortedStrings(scope.ProjectIDs),
		TaskIDs:     uniqueSortedStrings(scope.TaskIDs),
		Generations: uniqueSortedStrings(scope.Generations),
		ParentIDs:   uniqueSortedStrings(scope.ParentIDs),
	}
}

func uniqueActions(actions []DispatchAction) []DispatchAction {
	out := append([]DispatchAction(nil), actions...)
	slices.Sort(out)
	return slices.Compact(out)
}

func uniqueSortedStrings(values []string) []string {
	out := append([]string(nil), values...)
	slices.Sort(out)
	out = slices.Compact(out)
	return slices.DeleteFunc(out, func(v string) bool { return v == "" })
}

func scopesEqual(a, b DispatchHoldScope) bool {
	return equalStrings(a.ProjectIDs, b.ProjectIDs) &&
		equalStrings(a.TaskIDs, b.TaskIDs) &&
		equalStrings(a.Generations, b.Generations) &&
		equalStrings(a.ParentIDs, b.ParentIDs)
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
