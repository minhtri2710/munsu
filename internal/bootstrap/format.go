package bootstrap

import "fmt"

// String returns the CLI-layer display line for a tool diagnostic.
func (d ToolDiagnostic) String() string {
	switch d.Status {
	case ToolFound:
		return fmt.Sprintf("FOUND: %s at %s", d.Tool, d.Path)
	case ToolMissing:
		return fmt.Sprintf("MISSING: %s (install instructions vary)", d.Tool)
	case ToolInstallFailed:
		return fmt.Sprintf("INSTALL_FAILED: %s — %v", d.Tool, d.Err)
	case ToolInstalled:
		return fmt.Sprintf("INSTALLED: %s", d.Tool)
	default:
		return ""
	}
}

// String returns the CLI-layer display line for an auth diagnostic.
func (d AuthDiagnostic) String() string {
	switch d.Status {
	case AuthAuthenticated:
		return "GH_AUTH: authenticated"
	case AuthFailed:
		return "NEEDS_GH_AUTH: gh auth status failed (run gh auth login)"
	default:
		return ""
	}
}

// String returns the CLI-layer display line for a config diagnostic.
func (d ConfigDiagnostic) String() string {
	if d.Source != "" {
		return fmt.Sprintf("%s: %s (source: %s)", d.Key, d.Value, d.Source)
	}
	return fmt.Sprintf("%s: %s", d.Key, d.Value)
}

// String returns the CLI-layer display line for a GC diagnostic.
func (d GCDiagnostic) String() string {
	if d.SkippedReason != "" {
		return fmt.Sprintf("GC: skipped (%s)", d.SkippedReason)
	}
	return fmt.Sprintf("GC: removed %d orphan data dir(s): %v", d.Removed, d.Dirs)
}
