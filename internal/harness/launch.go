package harness

import "strings"

// shellQuote wraps a string in double quotes if it contains shell-special characters.
func shellQuote(s string) string {
	if strings.ContainsAny(s, " \t\n\r()\"'") {
		return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	return s
}

// LaunchString builds the shell command string to launch a harness agent.
func LaunchString(h string, tmpl Template) string {
	parts := []string{strings.ToLower(h)}
	for _, arg := range tmpl.ExtraArgs {
		parts = append(parts, shellQuote(arg))
	}
	if tmpl.ModelFlag != "" && tmpl.DefaultModel != "" {
		parts = append(parts, tmpl.ModelFlag, shellQuote(tmpl.DefaultModel))
	}
	if tmpl.EffortFlag != "" && tmpl.DefaultEffort != "" {
		parts = append(parts, tmpl.EffortFlag, tmpl.DefaultEffort)
	}
	return strings.Join(parts, " ")
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
