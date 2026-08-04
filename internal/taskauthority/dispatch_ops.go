package taskauthority

import (
	"sort"
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
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	result := out[:0]
	for _, action := range out {
		if len(result) == 0 || result[len(result)-1] != action {
			result = append(result, action)
		}
	}
	return result
}

func uniqueSortedStrings(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	result := out[:0]
	for _, value := range out {
		if value != "" && (len(result) == 0 || result[len(result)-1] != value) {
			result = append(result, value)
		}
	}
	return result
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
