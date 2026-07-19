package harness

import "strings"

// shellQuote wraps a string in double quotes if it contains shell-special characters.
func shellQuote(s string) string {
	if strings.ContainsAny(s, " \t\n\r()\"'") {
		return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	return s
}

// LaunchString builds the shell command string to launch a harness agent
// using the template defaults for model and effort.
func LaunchString(h string, tmpl Template) string {
	return LaunchStringWith(h, tmpl, tmpl.DefaultModel, tmpl.DefaultEffort)
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

// LaunchStringFromAdapter builds the launch string for a harness using its
// adapter from the registry. Returns empty string if the harness is unknown.
func LaunchStringFromAdapter(h string) string {
	a, ok := GetAdapter(h)
	if !ok {
		return ""
	}
	return LaunchString(h, a.LaunchTemplate)
}
