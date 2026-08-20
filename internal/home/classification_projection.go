package home

import (
	"github.com/minhtri2710/munsu/internal/domain"
	"os"
	"path/filepath"
	"strings"
)

func statusLines(path string) []string {
	b, e := os.ReadFile(path)
	if e != nil {
		return nil
	}
	return strings.Split(string(b), "\n")
}
func lastStatusLine(path string) string {
	l := statusLines(path)
	for i := len(l) - 1; i >= 0; i-- {
		if s := strings.TrimSpace(l[i]); s != "" {
			return s
		}
	}
	return ""
}
func OpenDecisions(path string) []domain.Decision { return domain.FoldOpenDecisions(statusLines(path)) }
func OpenActivities(path string) []domain.Activity {
	return domain.FoldOpenActivities(statusLines(path))
}
func AbsorbClass(id, stateDir string) domain.AbsorbResult {
	stem, err := DurableKey(id)
	if err != nil {
		return domain.None
	}
	return domain.ClassifyAbsorb(lastStatusLine(filepath.Join(stateDir, stem+".status")))
}
func ScanGeneralRelevant(stateDir string) []domain.StatusMatch {
	es, e := os.ReadDir(stateDir)
	if e != nil {
		return nil
	}
	var out []domain.StatusMatch
	for _, x := range es {
		if !strings.HasSuffix(x.Name(), ".status") {
			continue
		}
		id, err := ReverseDurableKey(strings.TrimSuffix(x.Name(), ".status"))
		if err != nil {
			continue
		}
		p := filepath.Join(stateDir, x.Name())
		line := lastStatusLine(p)
		if domain.GeneralRelevant(line) {
			out = append(out, domain.StatusMatch{Path: p, TaskID: id, LastLine: line})
		}
	}
	return out
}
