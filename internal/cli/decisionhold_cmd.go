package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
	"github.com/spf13/cobra"
)

func newDecisionHoldCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "decision-hold",
		Short: "Manage durable general decision holds",
		Long: `Manage the decision-hold lifecycle for unresolved general decisions
discovered during investigations or reviews.

Subcommands: hold, complete, verify, resolve, list.

Each decision gets one stable key. The hold remains open until the
general's answer is recorded and any dependent work is unblocked.`,
	}

	cmd.AddCommand(newDecisionHoldHoldCmd())
	cmd.AddCommand(newDecisionHoldCompleteCmd())
	cmd.AddCommand(newDecisionHoldVerifyCmd())
	cmd.AddCommand(newDecisionHoldResolveCmd())
	cmd.AddCommand(newDecisionHoldListCmd())

	return cmd
}

// decisionHoldID returns the stable Authority hold identity for a general
// decision: <origin-id>-decision-<decision-key>.
func decisionHoldID(originID, decisionKey string) string {
	return originID + "-decision-" + decisionKey
}

// holdActionSet is the conservative action set a general decision hold gates:
// a paused task must not be handed off, started, or spawned until the
// decision resolves.
var holdActionSet = []taskauthority.DispatchAction{
	taskauthority.DispatchActionStart,
	taskauthority.DispatchActionSpawn,
	taskauthority.DispatchActionHandoff,
}

// authorityHold returns the committed hold with the given ID, if any.
func authorityHold(auth *taskauthority.Canonical, id string) (taskauthority.DispatchHold, bool, error) {
	holds, err := auth.ListHolds()
	if err != nil {
		return taskauthority.DispatchHold{}, false, err
	}
	for _, hold := range holds {
		if hold.ID == id {
			return hold, true, nil
		}
	}
	return taskauthority.DispatchHold{}, false, nil
}

// holdsScopedToTask reports whether a hold gates the given task: a hold
// applies when its task scope contains the task or when the task scope is
// empty (empty scope fields match all tasks, ADR-0004 §7).
func holdsScopedToTask(hold taskauthority.DispatchHold, taskID string) bool {
	if len(hold.Scope.TaskIDs) == 0 {
		return true
	}
	for _, candidate := range hold.Scope.TaskIDs {
		if candidate == taskID {
			return true
		}
	}
	return false
}

// decisionKeyFromHold recovers the CLI decision key from a hold identity:
// <origin>-decision-<key> for CLI-created holds, otherwise the decision key
// behind an interpretation hold (ID = Key + "-hold").
func decisionKeyFromHold(originID string, hold taskauthority.DispatchHold) string {
	prefix := originID + "-decision-"
	if strings.HasPrefix(hold.ID, prefix) {
		return strings.TrimPrefix(hold.ID, prefix)
	}
	if strings.HasSuffix(hold.ID, "-hold") {
		return strings.TrimSuffix(hold.ID, "-hold")
	}
	return hold.ID
}

// unresolvedDecisionHolds returns the active (unreleased) Authority holds
// scoped to the origin task, sorted by ID.
func unresolvedDecisionHolds(auth *taskauthority.Canonical, originID string) ([]taskauthority.DispatchHold, error) {
	holds, err := auth.ListHolds()
	if err != nil {
		return nil, err
	}
	var unresolved []taskauthority.DispatchHold
	for _, hold := range holds {
		if hold.ReleasedAt != 0 {
			continue
		}
		if holdsScopedToTask(hold, originID) {
			unresolved = append(unresolved, hold)
		}
	}
	sort.Slice(unresolved, func(i, j int) bool { return unresolved[i].ID < unresolved[j].ID })
	return unresolved, nil
}

// resolveDecisionHold releases one decision hold through the canonical
// Authority: every hold this CLI creates is a durable DispatchHold released
// by the idempotent ReleaseHold operation. The legacy decision record path
// (ResolveDecision) was removed with the interpretation layer; there is no
// decision store on the canonical surface.
func resolveDecisionHold(ctx Ctx, auth *taskauthority.Canonical, originID, decisionKey, answer string) error {
	hid := decisionHoldID(originID, decisionKey)
	req := taskauthority.CanonicalReleaseHoldRequest{
		HomeID: auth.HomeID(),
		HoldID: hid,
		Reason: "decision resolved: " + answer,
	}
	op, err := newCanonicalOperation("decision-hold-release", req)
	if err != nil {
		return err
	}
	_, err = auth.ReleaseHold(op, req)
	return err
}

func newDecisionHoldHoldCmd() *cobra.Command {
	var reason string
	var from string

	cmd := &cobra.Command{
		Use:   "hold <key> --reason <summary> --from <task-id>",
		Short: "Record a new general decision hold",
		Long: `Record a new general decision hold.

Creates a durable Authority hold that blocks dispatch of the originating
task until the general resolves the decision. Idempotent: running with
the same key and origin task is a no-op.

Example:
  munsu decision-hold hold approach --reason "Pick the UI framework" --from scout-r2`,
		Args: ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			key := args[0]
			if reason == "" {
				return fmt.Errorf("--reason is required")
			}
			if from == "" {
				return fmt.Errorf("--from is required")
			}

			auth, err := ctx.TaskAuthority()
			if err != nil {
				return fmt.Errorf("decision holds require task authority: %w", err)
			}
			hid := decisionHoldID(from, key)

			// The typed contract distinguishes a fresh hold from an idempotent
			// repeat. An existing hold (any definition) is reported as already
			// present without a second Authority mutation.
			created := true
			if _, exists, err := authorityHold(auth, hid); err != nil {
				return fmt.Errorf("reading holds: %w", err)
			} else if exists {
				created = false
			} else {
				req := taskauthority.CanonicalAddHoldRequest{
					HomeID:  auth.HomeID(),
					HoldID:  hid,
					Scope:   taskauthority.DispatchHoldScope{TaskIDs: []string{from}},
					Actions: holdActionSet,
					Reason:  reason,
				}
				op, err := newCanonicalOperation("decision-hold", req)
				if err != nil {
					return fmt.Errorf("creating hold: %w", err)
				}
				if _, err := auth.AddHold(op, req); err != nil {
					return fmt.Errorf("creating hold: %w", err)
				}
			}

			// The status line is a post-commit projection (ADR-0007 §7): a
			// projection failure must not roll back the authoritative hold.
			if err := home.AppendStatus(ctx.Home, from, fmt.Sprintf("needs-decision: %s [key=%s]", reason, key)); err != nil {
				return fmt.Errorf("appending needs-decision status: %w", err)
			}

			if created {
				return writeContract(cmd, Response[MessageResult]{
					SchemaVersion: SchemaVersion,
					Kind:          "decision-hold.hold",
					Status:        "success",
					Data:          MessageResult{Message: fmt.Sprintf("Hold %s created on %s", hid, from)},
				})
			}
			return writeContract(cmd, Response[MessageResult]{
				SchemaVersion: SchemaVersion,
				Kind:          "decision-hold.hold",
				Status:        "success",
				Data:          MessageResult{Message: fmt.Sprintf("Hold %s already exists on %s (idempotent)", hid, from), Noop: true},
			})
		}),
	}
	configureContractCommand(cmd)

	cmd.Flags().StringVar(&reason, "reason", "", "One-line summary of the decision needed")
	cmd.Flags().StringVar(&from, "from", "", "Originating task ID that discovered this decision")

	return cmd
}

func newDecisionHoldCompleteCmd() *cobra.Command {
	var none bool

	cmd := &cobra.Command{
		Use:   "complete <origin-id> [<key>...]",
		Short: "Mark decisions as complete",
		Long: `Mark decisions discovered during an investigation or review as complete.

Accepts one or more decision keys. Use --none to attest that the reviewed
surface has no unresolved general decisions.

Examples:
  munsu decision-hold complete scout-r2 approach db-schema
  munsu decision-hold complete scout-r2 --none`,
		Args: MinimumNArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			originID := args[0]
			keys := args[1:]

			if none {
				if len(keys) > 0 {
					return fmt.Errorf("--none cannot be combined with explicit keys")
				}
				keys = []string{"--none"}
			}

			if len(keys) == 0 {
				return fmt.Errorf("specify at least one key or --none")
			}

			if len(keys) == 1 && keys[0] == "--none" {
				// Attestation is an ephemeral command outcome: nothing durable
				// is written when the reviewed surface has no decisions.
				return writeContract(cmd, Response[MessageResult]{
					SchemaVersion: SchemaVersion,
					Kind:          "decision-hold.complete",
					Status:        "success",
					Data:          MessageResult{Message: fmt.Sprintf("Attested no pending decisions for %s", originID)},
				})
			}

			auth, err := ctx.TaskAuthority()
			if err != nil {
				return fmt.Errorf("decision holds require task authority: %w", err)
			}
			for _, key := range keys {
				if err := resolveDecisionHold(ctx, auth, originID, key, "recorded (decision noted)"); err != nil {
					return fmt.Errorf("completing decision hold %s: %w", key, err)
				}
				// The status line is a post-commit projection (ADR-0007 §7),
				// mirroring the resolve path so the needs-decision line is not
				// left stale for verify or the scout retirement check.
				if err := home.AppendStatus(ctx.Home, originID, fmt.Sprintf("resolved: recorded (decision noted) [key=%s]", key)); err != nil {
					return fmt.Errorf("appending resolved status: %w", err)
				}
			}
			return writeContract(cmd, Response[MessageResult]{
				SchemaVersion: SchemaVersion,
				Kind:          "decision-hold.complete",
				Status:        "success",
				Data:          MessageResult{Message: fmt.Sprintf("Completed %d decision hold(s) for %s: %s", len(keys), originID, strings.Join(keys, ", "))},
			})
		}),
	}
	configureContractCommand(cmd)

	cmd.Flags().BoolVar(&none, "none", false, "Attest that no unresolved decisions exist")

	return cmd
}

// verifyDecisionHolds computes the unresolved decision keys for an origin
// task: active Authority holds scoped to the task, plus stale
// needs-decision status lines whose key has no resolved counterpart.
func verifyDecisionHolds(ctx Ctx, auth *taskauthority.Canonical, originID string, keys []string) ([]string, error) {
	unresolved := map[string]bool{}
	check := map[string]bool{}
	for _, key := range keys {
		check[key] = true
	}

	holds, err := auth.ListHolds()
	if err != nil {
		return nil, err
	}
	active := map[string]bool{}
	allKeys := map[string]bool{}
	for _, hold := range holds {
		if !holdsScopedToTask(hold, originID) {
			continue
		}
		allKeys[decisionKeyFromHold(originID, hold)] = true
		if hold.ReleasedAt == 0 {
			key := decisionKeyFromHold(originID, hold)
			active[key] = true
			unresolved[key] = true
		}
	}
	if len(check) == 0 {
		for key := range allKeys {
			check[key] = true
		}
	}

	// Status-line staleness: a needs-decision line without a resolved line
	// is unresolved unless the key is already resolved by an active-hold
	// absence. Active Authority holds dominate; released holds are resolved.
	statusLines, err := home.ReadStatus(ctx.Home, originID)
	if err != nil {
		return nil, fmt.Errorf("reading status for %s: %w", originID, err)
	}
	resolved := map[string]bool{}
	needs := map[string]bool{}
	for _, line := range statusLines {
		_, key := home.ParseStatusKey(line)
		if key == "" {
			continue
		}
		if strings.HasPrefix(line, "resolved:") {
			resolved[key] = true
		}
		if strings.HasPrefix(line, "needs-decision:") {
			needs[key] = true
		}
	}
	for key := range check {
		if active[key] || resolved[key] {
			continue
		}
		if needs[key] {
			unresolved[key] = true
		}
	}

	out := make([]string, 0, len(unresolved))
	for key := range unresolved {
		out = append(out, key)
	}
	sort.Strings(out)
	return out, nil
}

func newDecisionHoldVerifyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "verify <origin-id> [<key>...]",
		Short: "Verify no stale needs-decision holds remain",
		Long: `Verify that the originating task has no stale needs-decision status lines
and no active Authority decision holds.

When keys are provided, only those keys are checked. Without keys, all
holds for the origin task are checked.

Exit codes: 0 = clean, 1 = unresolved decisions found, 2 = error.

Example:
  munsu decision-hold verify scout-r2`,
		Args: MinimumNArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			originID := args[0]
			keys := args[1:]

			auth, err := ctx.TaskAuthority()
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "error: verifying holds: %v\n", err)
				exitWithCode(2)
				return nil
			}
			unresolvedKeys, err := verifyDecisionHolds(ctx, auth, originID, keys)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "error: verifying holds: %v\n", err)
				exitWithCode(2)
				return nil
			}

			if len(unresolvedKeys) > 0 {
				fmt.Fprintf(cmd.ErrOrStderr(), "unresolved decisions remain: %s\n", strings.Join(unresolvedKeys, ", "))
				exitWithCode(1)
				return nil
			}

			fmt.Fprintf(cmd.OutOrStdout(), "No unresolved decisions for %s\n", originID)
			return nil
		}),
	}

	return cmd
}

func newDecisionHoldResolveCmd() *cobra.Command {
	var answer string
	var unblock []string
	var from string
	cmd := &cobra.Command{
		Use:   "resolve <key> --answer <text> --from <origin-id> [--unblock <dep-id>...]",
		Short: "Record the general's decision and unblock dependent work",
		Long: `Record the general's decision for a hold and unblock any dependent tasks.

The --from flag specifies the originating task ID (must match the hold's origin).
The --unblock flag may be repeated to unblock multiple dependencies.

Examples:
  munsu decision-hold resolve approach --answer "Choose React" --from scout-r2
  munsu decision-hold resolve approach --answer "Choose React" --from scout-r2 --unblock dep-task-1`,
		Args: ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			key := args[0]
			if answer == "" {
				return fmt.Errorf("--answer is required")
			}
			if from == "" {
				return fmt.Errorf("--from is required")
			}

			auth, err := ctx.TaskAuthority()
			if err != nil {
				return fmt.Errorf("decision holds require task authority: %w", err)
			}
			if err := resolveDecisionHold(ctx, auth, from, key, answer); err != nil {
				return fmt.Errorf("resolving hold: %w", err)
			}

			// Status lines are post-commit projections (ADR-0007 §7).
			if err := home.AppendStatus(ctx.Home, from, fmt.Sprintf("resolved: %s [key=%s]", answer, key)); err != nil {
				return fmt.Errorf("appending resolved status: %w", err)
			}
			for _, depID := range unblock {
				if depID == "" {
					continue
				}
				if err := home.AppendStatus(ctx.Home, depID, fmt.Sprintf("unblocked: decision resolved [key=%s]", key)); err != nil {
					return fmt.Errorf("unblocking %s: %w", depID, err)
				}
			}

			msg := fmt.Sprintf("Hold %s resolved: %s", key, answer)
			if len(unblock) > 0 {
				msg += "\nUnblocked: " + strings.Join(unblock, ", ")
			}
			return writeContract(cmd, Response[MessageResult]{
				SchemaVersion: SchemaVersion,
				Kind:          "decision-hold.resolve",
				Status:        "success",
				Data:          MessageResult{Message: msg},
			})
		}),
	}
	configureContractCommand(cmd)

	cmd.Flags().StringVar(&answer, "answer", "", "The general's decision")
	cmd.Flags().StringVar(&from, "from", "", "Originating task ID that owns this decision hold")
	cmd.Flags().StringArrayVar(&unblock, "unblock", nil, "Dependent task to unblock (repeatable)")
	return cmd
}

func newDecisionHoldListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list <origin-id>",
		Short: "List unresolved decisions for an origin task",
		Args:  ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			originID := args[0]

			auth, err := ctx.TaskAuthority()
			if err != nil {
				return fmt.Errorf("decision holds require task authority: %w", err)
			}
			holds, err := unresolvedDecisionHolds(auth, originID)
			if err != nil {
				return fmt.Errorf("listing holds: %w", err)
			}

			if len(holds) == 0 {
				return writeContract(cmd, Response[EmptyResult]{
					SchemaVersion: SchemaVersion,
					Kind:          "decision-hold.list",
					Status:        "success",
					Data:          EmptyResult{Count: 0, Context: fmt.Sprintf("No unresolved decisions for %s", originID)},
				})
			}

			var holdEntries []DecisionHoldInfo
			for _, hold := range holds {
				holdEntries = append(holdEntries, DecisionHoldInfo{
					DecisionKey: decisionKeyFromHold(originID, hold),
					Reason:      hold.Reason,
				})
			}

			return writeContract(cmd, Response[[]DecisionHoldInfo]{
				SchemaVersion: SchemaVersion,
				Kind:          "decision-hold.list",
				Status:        "success",
				Data:          holdEntries,
			})
		}),
	}
	configureContractCommand(cmd)
	return cmd
}
