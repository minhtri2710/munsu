package fleet

import (
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

const taskPRURL = "https://github.com/org/repo/pull/42"

// TestReadTaskPR proves the read-time PR is the last PR URL the status log
// reports, marked unverified, and empty when no line names a PR.
func TestReadTaskPR(t *testing.T) {
	const id = "t1"
	reported := "reported: PR " + taskPRURL + " (unverified)"
	tests := []struct {
		name   string
		status []string
		want   string
	}{
		{name: "PR line", status: []string{"done: PR " + taskPRURL + " checks green"}, want: reported},
		{name: "newer non-PR URL does not hide the PR line",
			status: []string{"done: PR " + taskPRURL + " checks green", "blocked: see https://ci.example.com/log/9"}, want: reported},
		{name: "trailing punctuation is trimmed", status: []string{"done: PR (" + taskPRURL + "). green"}, want: reported},
		{name: "no PR anywhere", status: []string{"working: spawned", "blocked: see https://ci.example.com/log/9"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			homeDir := t.TempDir()
			for _, line := range tt.status {
				if err := home.AppendStatus(homeDir, id, line); err != nil {
					t.Fatal(err)
				}
			}
			if got := ReadTaskPR(homeDir, id).Line(); got != tt.want {
				t.Fatalf("Line() = %q, want %q", got, tt.want)
			}
		})
	}
}
