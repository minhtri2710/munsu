package bootstrap

// ToolSpec defines a tool that bootstrap checks for presence.
type ToolSpec struct {
	Name     string
	Required bool
}

// checkedTools are the tools every home needs. The session backend's binary
// is checked beside them only once a backend is resolved; its name is the
// backend identity (herdr or tmux).
var checkedTools = []ToolSpec{
	{"git", true},
	{"gh-axi", false},
	{"gh", false},
	{"fuser", false},
}
