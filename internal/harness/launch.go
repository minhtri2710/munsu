package harness

import "strings"

// shellQuote wraps a string in double quotes if it contains shell-special characters.
func shellQuote(s string) string {
	if strings.ContainsAny(s, " \t\n\r()\"'") {
		return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	return s
}

// LaunchStringWith builds the shell command using explicit model/effort overrides.
// Empty model/effort omit the corresponding flags (including the "default" sentinel).
// The sentinel value "default" is treated as empty.
func LaunchStringWith(h string, tmpl Template, model, effort string) string {
	parts := []string{strings.ToLower(h)}
	for _, arg := range tmpl.ExtraArgs {
		parts = append(parts, shellQuote(arg))
	}
	model = normalizeLaunchToken(model)
	effort = normalizeLaunchToken(effort)
	if tmpl.ModelFlag != "" && model != "" {
		parts = append(parts, tmpl.ModelFlag, shellQuote(model))
	}
	if tmpl.EffortFlag != "" && effort != "" {
		parts = append(parts, tmpl.EffortFlag, shellQuote(effort))
	}
	return strings.Join(parts, " ")
}

func normalizeLaunchToken(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || s == "default" {
		return ""
	}
	return s
}
