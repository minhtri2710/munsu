package fleet

import "path/filepath"

func taskHandoffProjectionRelPaths(taskID string) []string {
	return []string{
		filepath.Join("state", taskID+".meta"),
		filepath.Join("state", taskID+".status"),
		filepath.Join("data", taskID, "brief.md"),
	}
}
