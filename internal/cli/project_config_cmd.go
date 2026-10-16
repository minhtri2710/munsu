package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/harness"
	"github.com/spf13/cobra"
)

// projectOverlayKey is one scalar overlay field the `project config` surface
// reads and writes. It is the only production writer of the Config-owned
// project overlay document. The structured DispatchProfiles field and the
// consumer-less DispatchAutonomy field are intentionally excluded from this
// surface.
type projectOverlayKey struct {
	// get returns the persisted overlay value and whether the field is set. An
	// unset field reports empty success, mirroring `munsu config get`.
	get func(config.ProjectOverlay) (value string, set bool)
	// set validates value and applies it to the overlay. An empty value clears
	// the field so the Project returns to inheriting the fleet base document.
	set func(o *config.ProjectOverlay, value string) error
	// check, when set, validates the overlay against the home after set applied
	// the value and before the overlay is stored.
	check func(home string, o config.ProjectOverlay) error
}

func toolEntryKey(get func(config.ProjectOverlay) *config.ToolEntry, set func(*config.ProjectOverlay, *config.ToolEntry)) projectOverlayKey {
	return projectOverlayKey{
		get: func(o config.ProjectOverlay) (string, bool) {
			entry := get(o)
			if entry == nil {
				return "", false
			}
			// ToolEntry holds only strings, so Marshal cannot fail.
			data, _ := json.Marshal(entry)
			return string(data), true
		},
		set: func(o *config.ProjectOverlay, value string) error {
			if strings.TrimSpace(value) == "" {
				set(o, nil)
			} else {
				var entry config.ToolEntry
				decoder := json.NewDecoder(strings.NewReader(value))
				decoder.DisallowUnknownFields()
				if err := decoder.Decode(&entry); err != nil {
					return usageError("invalid_value", "Pass a JSON tool entry", err.Error())
				}
				var trailing any
				if err := decoder.Decode(&trailing); err != io.EOF {
					return usageError("invalid_value", "Pass exactly one JSON tool entry", "trailing JSON after tool entry")
				}
				set(o, &entry)
			}
			if err := config.ValidateProjectTools(*o); err != nil {
				return usageError("invalid_value", "Pass a supported tool entry", err.Error())
			}
			return nil
		},
	}
}

var projectOverlayKeys = map[string]projectOverlayKey{
	"soldier-harness": {
		get: func(o config.ProjectOverlay) (string, bool) { return o.SoldierHarness, o.SoldierHarness != "" },
		set: func(o *config.ProjectOverlay, value string) error {
			value = strings.TrimSpace(value)
			if value != "" {
				if err := harness.ValidateHarness(value); err != nil {
					return usageError("invalid_value", "Pass a supported harness name", err.Error())
				}
			}
			o.SoldierHarness = value
			return nil
		},
	},
	"model": {
		get: func(o config.ProjectOverlay) (string, bool) { return o.Model, o.Model != "" },
		set: func(o *config.ProjectOverlay, value string) error { o.Model = strings.TrimSpace(value); return nil },
	},
	"backend": {
		get: func(o config.ProjectOverlay) (string, bool) { return o.Backend, o.Backend != "" },
		set: func(o *config.ProjectOverlay, value string) error { o.Backend = strings.TrimSpace(value); return nil },
	},
	"tamper-check": {
		get: func(o config.ProjectOverlay) (string, bool) { return o.TamperCheck, o.TamperCheck != "" },
		set: func(o *config.ProjectOverlay, value string) error {
			if i := strings.IndexAny(value, "\r\n`"); i >= 0 {
				return usageError("invalid_value", "Pass the command on one line without backticks", fmt.Sprintf("tamper-check must not contain a carriage return, line feed or backtick, got %q", value[i]))
			}
			o.TamperCheck = strings.TrimSpace(value)
			return nil
		},
	},
	"fence-unsupported": {
		get: func(o config.ProjectOverlay) (string, bool) { return o.FenceUnsupported, o.FenceUnsupported != "" },
		set: func(o *config.ProjectOverlay, value string) error {
			value = strings.TrimSpace(value)
			if value != "" && value != "allow" {
				return usageError("invalid_value", "Pass allow, or an empty value to clear it", fmt.Sprintf("fence-unsupported must be allow, got %q", value))
			}
			o.FenceUnsupported = value
			return nil
		},
	},
	"fence-writable-roots": {
		get: func(o config.ProjectOverlay) (string, bool) {
			return strings.Join(o.FenceWritableRoots, "\n"), len(o.FenceWritableRoots) > 0
		},
		set: func(o *config.ProjectOverlay, value string) error {
			o.FenceWritableRoots = nil
			for _, line := range strings.Split(value, "\n") {
				if line = strings.TrimSpace(line); line != "" {
					o.FenceWritableRoots = append(o.FenceWritableRoots, line)
				}
			}
			return nil
		},
		check: func(home string, o config.ProjectOverlay) error {
			if len(o.FenceWritableRoots) == 0 {
				return nil
			}
			if err := fleet.ValidateFenceWritableRoots(home, o.FenceWritableRoots); err != nil {
				return usageError("invalid_value", "Pass one absolute directory per line, outside every checkout and tool directory", err.Error())
			}
			return nil
		},
	},
	"forge":    toolEntryKey(func(o config.ProjectOverlay) *config.ToolEntry { return o.Forge }, func(o *config.ProjectOverlay, entry *config.ToolEntry) { o.Forge = entry }),
	"worktree": toolEntryKey(func(o config.ProjectOverlay) *config.ToolEntry { return o.Worktree }, func(o *config.ProjectOverlay, entry *config.ToolEntry) { o.Worktree = entry }),
	"quota":    toolEntryKey(func(o config.ProjectOverlay) *config.ToolEntry { return o.Quota }, func(o *config.ProjectOverlay, entry *config.ToolEntry) { o.Quota = entry }),
}

// projectOverlayKeyNames returns the settable overlay keys in stable order for
// help text and refusal messages.
func projectOverlayKeyNames() []string {
	names := make([]string, 0, len(projectOverlayKeys))
	for k := range projectOverlayKeys {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

func unknownProjectOverlayKey(key string) error {
	return usageError("unknown_key", "Pass one of: "+strings.Join(projectOverlayKeyNames(), ", "), fmt.Sprintf("unknown project overlay key %q", key))
}

func newProjectConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Read and write a project's config overlay",
		Long: `Read and write one registered project's Config-owned overlay values.

An overlay value shadows the fleet base document (config/base.json) for that
one project. Set an empty value to clear a key and return the project to
inheriting the base value. Overlays take effect at the General's next spawn
resolution; captains observe them at their next config-push.

Overlay keys: ` + strings.Join(projectOverlayKeyNames(), ", ") + `.
`,
	}

	getCmd := &cobra.Command{
		Use:   "get <name> <key>",
		Short: "Get a project overlay value",
		Args:  ExactArgs(2),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			name, key := args[0], args[1]
			spec, ok := projectOverlayKeys[key]
			if !ok {
				return unknownProjectOverlayKey(key)
			}
			if _, err := fleet.Find(ctx.Home, name); err != nil {
				return err
			}
			overlay, err := config.LoadProjectOverlay(ctx.Home, name)
			if err != nil {
				return err
			}
			value, set := spec.get(overlay)
			if !set {
				return nil
			}
			return writeContract(cmd, Response[MessageResult]{
				SchemaVersion: SchemaVersion,
				Kind:          "message",
				Status:        "success",
				Data:          MessageResult{Message: value},
			})
		}),
	}
	configureContractCommand(getCmd)

	setCmd := &cobra.Command{
		Use:   "set <name> <key> <value>",
		Short: "Set a project overlay value (empty value clears it)",
		Args:  ExactArgs(3),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			name, key, value := args[0], args[1], args[2]
			spec, ok := projectOverlayKeys[key]
			if !ok {
				return unknownProjectOverlayKey(key)
			}
			if _, err := fleet.Find(ctx.Home, name); err != nil {
				return err
			}
			overlay, err := config.LoadProjectOverlay(ctx.Home, name)
			if err != nil {
				return err
			}
			if err := spec.set(&overlay, value); err != nil {
				return err
			}
			if spec.check != nil {
				if err := spec.check(ctx.Home, overlay); err != nil {
					return err
				}
			}
			return config.StoreProjectOverlay(ctx.Home, name, overlay)
		}),
	}
	configureContractCommand(setCmd)

	cmd.AddCommand(getCmd)
	cmd.AddCommand(setCmd)
	return cmd
}
