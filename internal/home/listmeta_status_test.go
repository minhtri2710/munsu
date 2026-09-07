package home

import "testing"

// TestListMetaLastStatusComesFromStatusFileAlone pins the post-cut contract of
// ListMeta: MetaEntry.LastStatus is the last line of the task's .status file
// with its [key=...] annotation stripped, and nothing in .meta can substitute
// for it. Before the delivery_state projection was deleted, a meta field could
// override that value, so a listing could show a lifecycle string the status
// file had never recorded. The claim here is stated over what ListMeta reads
// today rather than over the removed field, so it stays true and checkable
// after the removal instead of pinning its own premise.
func TestListMetaLastStatusComesFromStatusFileAlone(t *testing.T) {
	tests := []struct {
		name   string
		status []string
		meta   map[string]string
		want   string
	}{
		{
			name:   "last status line wins over earlier ones",
			status: []string{"running: building", "done: merged"},
			meta:   map[string]string{"kind": "ship"},
			want:   "done: merged",
		},
		{
			name:   "terminal-looking meta values never become the status",
			status: []string{"running: still working"},
			meta:   map[string]string{"kind": "ship", "mode": "merged", "harness": "delivered"},
			want:   "running: still working",
		},
		{
			name:   "no status file leaves the status empty however full the meta is",
			status: nil,
			meta:   map[string]string{"kind": "ship", "mode": "merged", "project": "munsu"},
			want:   "",
		},
		{
			name:   "the key annotation is stripped from the surfaced line",
			status: []string{"running: awaiting review [key=review]"},
			meta:   map[string]string{"kind": "ship"},
			want:   "running: awaiting review",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmp := t.TempDir()
			const id = "t1"
			if err := WriteMeta(tmp, id, tt.meta); err != nil {
				t.Fatalf("WriteMeta: %v", err)
			}
			for _, line := range tt.status {
				if err := AppendStatus(tmp, id, line); err != nil {
					t.Fatalf("AppendStatus(%q): %v", line, err)
				}
			}

			entries, err := ListMeta(tmp)
			if err != nil {
				t.Fatalf("ListMeta: %v", err)
			}
			if len(entries) != 1 {
				t.Fatalf("ListMeta returned %d entries, want 1: %v", len(entries), entries)
			}
			if entries[0].LastStatus != tt.want {
				t.Errorf("LastStatus = %q, want %q", entries[0].LastStatus, tt.want)
			}
		})
	}
}
