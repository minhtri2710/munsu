package cli

import (
	"fmt"
	"sort"
	"strconv"
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
}

// clearableBool builds an overlay key for a *bool field: an empty value clears
// it (nil, inherit base), any other value must parse as a bool.
func clearableBool(get func(config.ProjectOverlay) *bool, set func(*config.ProjectOverlay, *bool)) projectOverlayKey {
	return projectOverlayKey{
		get: func(o config.ProjectOverlay) (string, bool) {
			p := get(o)
			if p == nil {
				return "", false
			}
			return strconv.FormatBool(*p), true
		},
		set: func(o *config.ProjectOverlay, value string) error {
			if strings.TrimSpace(value) == "" {
				set(o, nil)
				return nil
			}
			parsed, err := strconv.ParseBool(strings.TrimSpace(value))
			if err != nil {
				return usageError("invalid_value", "Pass true or false", fmt.Sprintf("want true or false, got %q", value))
			}
			set(o, &parsed)
			return nil
		},
	}
}

var projectOverlayKeys = map[string]projectOverlayKey{
	"default-mode": {
		get: func(o config.ProjectOverlay) (string, bool) { return o.DefaultMode, o.DefaultMode != "" },
		set: func(o *config.ProjectOverlay, value string) error {
			value = strings.TrimSpace(value)
			if err := fleet.ValidateDeliveryMode(value); err != nil {
				return usageError("invalid_value", "Pass one of: no-mistakes, direct-PR, local-only", err.Error())
			}
			o.DefaultMode = value
			return nil
		},
	},
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
	"require-no-mistakes": clearableBool(
		func(o config.ProjectOverlay) *bool { return o.RequireNoMistakes },
		func(o *config.ProjectOverlay, v *bool) { o.RequireNoMistakes = v },
	),
	"allow-direct-pr-fallback": clearableBool(
		func(o config.ProjectOverlay) *bool { return o.AllowDirectPRFallback },
		func(o *config.ProjectOverlay, v *bool) { o.AllowDirectPRFallback = v },
	),
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
			return config.StoreProjectOverlay(ctx.Home, name, overlay)
		}),
	}
	configureContractCommand(setCmd)

	cmd.AddCommand(getCmd)
	cmd.AddCommand(setCmd)
	return cmd
}
