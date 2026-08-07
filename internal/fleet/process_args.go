package fleet

import (
	"strings"
)

func splitProcessCommand(command string) []string {
	var args []string
	var b strings.Builder
	quote := rune(0)
	escaped := false
	flush := func() {
		if b.Len() > 0 {
			args = append(args, b.String())
			b.Reset()
		}
	}
	for _, r := range command {
		if escaped {
			b.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				b.WriteRune(r)
			}
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			continue
		}
		if r == ' ' || r == '\t' {
			flush()
			continue
		}
		b.WriteRune(r)
	}
	flush()
	return args
}

func writerKindForArgs(args []string, expectedHome string) string {
	if len(args) < 2 {
		return ""
	}
	kind := ""
	for _, arg := range args[1:] {
		if arg == "watch" {
			kind = "watcher"
		}
		if arg == "afk" {
			kind = "afk"
		}
	}
	if kind == "" {
		return ""
	}
	for i := 1; i < len(args); i++ {
		value := ""
		if args[i] == "--home" && i+1 < len(args) {
			value = args[i+1]
		}
		if len(args[i]) > 7 && args[i][:7] == "--home=" {
			value = args[i][7:]
		}
		if value == "" {
			continue
		}
		canonical, err := canonicalHome(value)
		if err == nil && canonical == expectedHome {
			return kind
		}
	}
	return ""
}
