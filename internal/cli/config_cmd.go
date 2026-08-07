package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/harness"
	"github.com/spf13/cobra"
)

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Read, write, and view munsu configuration",
		Long: `Read, write, and view munsu configuration.

Configuration values are stored as files under $MUNSU_HOME/config/<key>,
except the typed operational keys (backend, default-mode,
require-no-mistakes), which are authored in the fleet base document
(config/base.json), the single operational authority. backend reports the
persisted snapshot Backend (the published config snapshot or the fleet base
document's typed Backend); the remaining keys report the persisted flat file
value.

Known config keys: ` + strings.Join(config.KnownKeys, ", ") + `.
`,
	}
	getCmd := &cobra.Command{
		Use:   "get <key>",
		Short: "Get a configuration value",
		Args:  ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			key := args[0]
			// backend is a persisted snapshot identity (published snapshot or fleet
			// base document's typed Backend), never a live env/PATH probe. Report
			// the persisted snapshot Backend, or a typed missing-input when none is
			// persisted.
			switch key {
			case "backend":
				resolved, err := fleet.ResolveGeneralHomeBackend(ctx.Home)
				if err != nil || resolved == "" {
					return usageError("missing_input", "Set backend in the fleet base config and rerun `munsu config get backend`", "no persisted backend identity is available")
				}
				return writeContract(cmd, Response[MessageResult]{
					SchemaVersion: SchemaVersion,
					Kind:          "message",
					Status:        "success",
					Data:          MessageResult{Message: resolved},
				})
			case "default-mode", "require-no-mistakes", "allow-direct-pr-fallback":
				// The fleet base document is the single operational authority for
				// the delivery-mode contract; report the persisted typed value.
				// A known-unset key reports empty success (the flat known-unset
				// contract); a malformed document fails closed.
				val, ok, err := readBaseConfigField(ctx.Home, key)
				if err != nil {
					return err
				}
				if !ok {
					return nil
				}
				return writeContract(cmd, Response[MessageResult]{
					SchemaVersion: SchemaVersion,
					Kind:          "message",
					Status:        "success",
					Data:          MessageResult{Message: val},
				})
			}
			val, err := config.Get(ctx.Home, key)
			if err != nil {
				if config.IsKnownKey(key) {
					return nil
				}
				return err
			}
			return writeContract(cmd, Response[MessageResult]{
				SchemaVersion: SchemaVersion,
				Kind:          "message",
				Status:        "success",
				Data:          MessageResult{Message: val},
			})
		}),
	}
	configureContractCommand(getCmd)
	cmd.AddCommand(getCmd)

	setCmd := &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set a configuration value",
		Args:  ExactArgs(2),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			key, value := args[0], args[1]
			// Validate harness pins. captain-harness accepts multi-token
			// lines: "<harness> [<model>] [<effort>]". soldier-harness is bare name only.
			switch key {
			case "soldier-harness":
				if err := harness.ValidateHarness(value); err != nil {
					return fmt.Errorf("config set %s: %w", key, err)
				}
			case "captain-harness":
				prof := harness.ParseHarnessLine(value)
				if prof.Harness == "" && strings.TrimSpace(value) != "" && strings.TrimSpace(value) != "default" {
					// bare "default" is allowed (unset sentinel)
					if !strings.HasPrefix(strings.TrimSpace(value), "#") {
						return fmt.Errorf("config set %s: empty harness token in %q (want \"<harness> [<model>] [<effort>]\")", key, value)
					}
				}
				if prof.Harness != "" {
					if err := harness.ValidateHarness(prof.Harness); err != nil {
						return fmt.Errorf("config set %s: %w", key, err)
					}
				}
				// Authoring boundary: write the captain launch profile into the
				// fleet base document (config/base.json). The base.json
				// CaptainProfile is the ONLY source consumed by captain
				// operations; the flat file remains a diagnostics-only echo.
				if err := setCaptainProfileInBase(ctx.Home, config.CaptainProfile{Harness: prof.Harness, Model: prof.Model, Effort: prof.Effort}); err != nil {
					return err
				}
				return config.Set(ctx.Home, key, value)
			case "model-allowlist":
				// One <harness>:<model> identity per line; empty (deny-all) is allowed.
				if err := harness.ValidateModelAllowlist(value); err != nil {
					return fmt.Errorf("config set %s: %w", key, err)
				}
			case "default-mode":
				if err := fleet.ValidateDeliveryMode(value); err != nil {
					return fmt.Errorf("config set default-mode: %w", err)
				}
				return setBaseConfigField(ctx.Home, func(b *config.FleetBaseDocument) { b.Config.DefaultMode = value })
			case "require-no-mistakes":
				parsed, err := strconv.ParseBool(strings.TrimSpace(value))
				if err != nil {
					return fmt.Errorf("config set require-no-mistakes: want true or false, got %q", value)
				}
				return setBaseConfigField(ctx.Home, func(b *config.FleetBaseDocument) {
					v := parsed
					b.Config.RequireNoMistakes = &v
				})
			case "backend":
				if strings.TrimSpace(value) == "" {
					return fmt.Errorf("config set backend: backend identity must not be empty")
				}
				return setBaseConfigField(ctx.Home, func(b *config.FleetBaseDocument) { b.Config.Backend = value })
			}
			return config.Set(ctx.Home, key, value)
		}),
	}
	cmd.AddCommand(setCmd)
	cmd.AddCommand(newConfigShowCmd())
	cmd.AddCommand(newConfigDispatchCmd())
	return cmd
}

// setCaptainProfileInBase writes the captain launch profile into the fleet
// base document (config/base.json), creating the document when absent and
// preserving all other fields. A malformed/invalid existing document fails
// closed (no self-repair). The base.json CaptainProfile is the ONLY captain
// operation source; the flat config/captain-harness file is a
// diagnostics-only echo.
func setCaptainProfileInBase(homeDir string, prof config.CaptainProfile) error {
	baseDoc, err := config.LoadFleetBase(homeDir)
	if err != nil {
		if _, statErr := os.Stat(filepath.Join(homeDir, config.BaseDocumentPath)); statErr == nil {
			return fmt.Errorf("config set captain-harness: loading fleet base document: %w", err)
		} else if !os.IsNotExist(statErr) {
			return statErr
		}
		baseDoc = config.FleetBaseDocument{SchemaVersion: config.FleetBaseSchemaVersion}
	}
	baseDoc.CaptainProfile = prof
	if err := config.StoreFleetBase(homeDir, baseDoc); err != nil {
		return fmt.Errorf("config set captain-harness: writing fleet base captainProfile: %w", err)
	}
	return nil
}

// setBaseConfigField applies one typed Config overlay field to the fleet base
// document (config/base.json), creating the document when absent and
// preserving all other fields. The base document is the single operational
// authority for the typed config surface; a malformed/invalid existing
// document fails closed (no self-repair).
func setBaseConfigField(homeDir string, mutate func(*config.FleetBaseDocument)) error {
	baseDoc, err := config.LoadFleetBase(homeDir)
	if err != nil {
		if _, statErr := os.Stat(filepath.Join(homeDir, config.BaseDocumentPath)); statErr == nil {
			return fmt.Errorf("loading fleet base document: %w", err)
		} else if !os.IsNotExist(statErr) {
			return statErr
		}
		baseDoc = config.FleetBaseDocument{SchemaVersion: config.FleetBaseSchemaVersion}
	}
	mutate(&baseDoc)
	if err := config.StoreFleetBase(homeDir, baseDoc); err != nil {
		return fmt.Errorf("writing fleet base document: %w", err)
	}
	return nil
}

// readBaseConfigField reads one typed fleet base config field (default-mode,
// require-no-mistakes, backend). ok is false when the field is unset or the
// base document is absent (known-unset); a malformed/invalid document fails
// closed.
func readBaseConfigField(homeDir, key string) (val string, ok bool, err error) {
	base, err := config.LoadFleetBase(homeDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	switch key {
	case "default-mode":
		return base.Config.DefaultMode, base.Config.DefaultMode != "", nil
	case "require-no-mistakes":
		if base.Config.RequireNoMistakes == nil {
			return "", false, nil
		}
		return strconv.FormatBool(*base.Config.RequireNoMistakes), true, nil
	case "backend":
		return base.Config.Backend, base.Config.Backend != "", nil
	}
	return "", false, nil
}

func newConfigShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show resolved configuration values with source",
		Args:  NoArgs,
		Long: `Display all persisted configuration values and their source.

Each value is read from the config file at $MUNSU_HOME/config/<key>.
Values that are not set are shown as "<not set>".
`,
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			return writeContract(cmd, Response[MessageResult]{
				SchemaVersion: SchemaVersion,
				Kind:          "config.show",
				Status:        "success",
				Data:          MessageResult{Message: showConfig(ctx.Home)},
			})
		}),
	}
	configureContractCommand(cmd)
	return cmd
}

// showConfig returns all well-known configuration values with their source.
func showConfig(homeDir string) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%-30s %s\n", "KEY", "VALUE"))
	b.WriteString(strings.Repeat("-", 80) + "\n")

	// Home path
	b.WriteString(fmt.Sprintf("%-30s %s\n", "home", homeDir))

	for _, key := range config.KnownKeys {
		// Typed operational keys live in the fleet base document (or the
		// published snapshot for backend), the single operational authority;
		// the legacy flat files are debris and are never read.
		switch key {
		case "backend":
			val, err := fleet.ResolveGeneralHomeBackend(homeDir)
			if err != nil || val == "" {
				b.WriteString(fmt.Sprintf("%-30s <not set>\n", key))
			} else {
				b.WriteString(fmt.Sprintf("%-30s %s (typed config)\n", key, val))
			}
			continue
		case "default-mode", "require-no-mistakes", "allow-direct-pr-fallback":
			val, ok, err := readBaseConfigField(homeDir, key)
			if err != nil || !ok {
				b.WriteString(fmt.Sprintf("%-30s <not set>\n", key))
			} else {
				b.WriteString(fmt.Sprintf("%-30s %s (typed config)\n", key, val))
			}
			continue
		}
		val, err := config.Get(homeDir, key)
		if err != nil {
			b.WriteString(fmt.Sprintf("%-30s <not set>\n", key))
			continue
		}
		b.WriteString(fmt.Sprintf("%-30s %s (file: %s)\n", key, val, config.ConfigDir(homeDir)+"/"+key))
	}

	// Show additional keys that happen to exist.
	additionalKeys := findExtraConfigKeys(homeDir)
	if len(additionalKeys) > 0 {
		b.WriteString("\nAdditional config keys:\n")
		for _, key := range additionalKeys {
			val, err := config.Get(homeDir, key)
			if err == nil {
				b.WriteString(fmt.Sprintf("  %-26s %s\n", key, val))
			}
		}
	}
	return strings.TrimSpace(b.String())
}

// findExtraConfigKeys lists config files that are not in the well-known list.
func findExtraConfigKeys(homeDir string) []string {
	known := make(map[string]bool, len(config.KnownKeys))
	for _, k := range config.KnownKeys {
		known[k] = true
	}
	entries, err := os.ReadDir(config.ConfigDir(homeDir))
	if err != nil {
		return nil
	}
	var extra []string
	for _, e := range entries {
		if !e.IsDir() && !known[e.Name()] {
			extra = append(extra, e.Name())
		}
	}
	sort.Strings(extra)
	return extra
}
