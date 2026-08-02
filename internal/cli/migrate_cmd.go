package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/minhtri2710/munsu/internal/configmigration"
	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthorityfs"
	"github.com/spf13/cobra"
)

func newMigrateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Run explicit state migrations",
	}
	cmd.AddCommand(newMigrateWakeResolutionsCmd())
	cmd.AddCommand(newMigrateTaskAuthorityCmd())
	cmd.AddCommand(newMigrateConfigCmd())
	return cmd
}

func newMigrateWakeResolutionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "wake-resolutions",
		Short: "Migrate legacy wake resolution state",
	}
	planCmd := &cobra.Command{
		Use:   "plan",
		Short: "Plan one-home wake resolution migration without source mutation",
		Args:  contractNoArgs,
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			if _, err := contractOutput(cmd); err != nil {
				return err
			}
			planPath, _ := cmd.Flags().GetString("plan-out")
			if planPath == "" {
				return usageError("invalid_argument", "Run `munsu migrate wake-resolutions plan --plan-out <plan.json>`", "--plan-out is required")
			}
			plan, err := home.PlanWakeResolutionMigration(ctx.Home)
			if err != nil {
				return operationError("invalid_argument", home.WakeResolutionMigrationCommand(ctx.Home), err.Error())
			}
			if err := home.WriteWakeResolutionMigrationPlan(planPath, plan); err != nil {
				return operationError("internal", home.WakeResolutionMigrationCommand(ctx.Home), err.Error())
			}
			message := fmt.Sprintf("Planned %d wake resolution record(s); digest=%s; plan=%s; apply=munsu migrate wake-resolutions apply --plan %s", plan.RecordCount, plan.SourceDigest, planPath, shellQuote(planPath))
			return writeContract(cmd, Response[MessageResult]{
				SchemaVersion: SchemaVersion,
				Kind:          "migrate.wake_resolutions.plan",
				Status:        "success",
				Data:          MessageResult{Message: message},
			})
		}),
	}
	planCmd.Flags().String("plan-out", "", "Path to write reviewed wake resolution migration plan JSON")
	configureContractCommand(planCmd)
	apply := &cobra.Command{
		Use:   "apply",
		Short: "Apply one reviewed wake resolution migration plan",
		Args:  contractNoArgs,
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			if _, err := contractOutput(cmd); err != nil {
				return err
			}
			planPath, _ := cmd.Flags().GetString("plan")
			if planPath == "" {
				return usageError("invalid_argument", "Run `munsu migrate wake-resolutions plan` first, then `munsu migrate wake-resolutions apply --plan <plan.json>`", "--plan is required")
			}
			plan, err := home.ReadWakeResolutionMigrationPlan(planPath)
			if err != nil {
				return operationError("invalid_argument", "Run `munsu migrate wake-resolutions plan` again", err.Error())
			}
			receipt, err := home.ApplyWakeResolutionMigration(plan)
			if err != nil {
				return operationError("internal", "Re-run the same `munsu migrate wake-resolutions apply --plan <plan.json>` after fixing the reported state", err.Error())
			}
			message := fmt.Sprintf("Migrated %d wake resolution record(s); digest=%s", receipt.RecordCount, receipt.SourceDigest)
			return writeContract(cmd, Response[MessageResult]{
				SchemaVersion: SchemaVersion,
				Kind:          "migrate.wake_resolutions.apply",
				Status:        "success",
				Data:          MessageResult{Message: message},
			})
		}),
	}
	apply.Flags().String("plan", "", "Path to reviewed wake resolution migration plan JSON")
	configureContractCommand(apply)
	fleetPlan := &cobra.Command{
		Use:   "fleet-plan",
		Short: "Plan fleet wake resolution migration without target-home mutation",
		Args:  contractNoArgs,
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			if _, err := contractOutput(cmd); err != nil {
				return err
			}
			planPath, _ := cmd.Flags().GetString("plan-out")
			if planPath == "" {
				return usageError("invalid_argument", "Run `munsu migrate wake-resolutions fleet-plan --plan-out <plan.json>`", "--plan-out is required")
			}
			plan := fleet.PlanFleetWakeResolutionMigration(ctx.Home)
			if err := fleet.WriteWakeResolutionFleetPlan(planPath, plan); err != nil {
				return operationError("internal", "Run the fleet plan command again", err.Error())
			}
			return writeContract(cmd, Response[fleet.WakeResolutionFleetPlan]{
				SchemaVersion: SchemaVersion,
				Kind:          "migrate.wake_resolutions.fleet_plan",
				Status:        "success",
				Data:          plan,
			})
		}),
	}
	fleetPlan.Flags().String("plan-out", "", "Path to write reviewed fleet wake resolution migration plan JSON")
	configureContractCommand(fleetPlan)
	fleetApply := &cobra.Command{
		Use:   "fleet-apply",
		Short: "Apply one reviewed fleet wake resolution migration plan",
		Args:  contractNoArgs,
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			if _, err := contractOutput(cmd); err != nil {
				return err
			}
			planPath, _ := cmd.Flags().GetString("plan")
			if planPath == "" {
				return usageError("invalid_argument", "Run `munsu migrate wake-resolutions fleet-plan` first, then `munsu migrate wake-resolutions fleet-apply --plan <plan.json>`", "--plan is required")
			}
			plan, err := fleet.ReadWakeResolutionFleetPlan(planPath)
			if err != nil {
				return operationError("invalid_argument", "Run `munsu migrate wake-resolutions fleet-plan` again", err.Error())
			}
			result := fleet.ApplyFleetWakeResolutionMigration(plan)
			return writeContract(cmd, Response[fleet.WakeResolutionFleetPlan]{
				SchemaVersion: SchemaVersion,
				Kind:          "migrate.wake_resolutions.fleet_apply",
				Status:        "success",
				Data:          result,
			})
		}),
	}
	fleetApply.Flags().String("plan", "", "Path to reviewed fleet wake resolution migration plan JSON")
	configureContractCommand(fleetApply)
	cmd.AddCommand(planCmd)
	cmd.AddCommand(apply)
	cmd.AddCommand(fleetPlan)
	cmd.AddCommand(fleetApply)
	return cmd
}

func newMigrateTaskAuthorityCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "task-authority",
		Short: "Migrate legacy v1 task authority state to the v2 authority schema",
	}
	planCmd := &cobra.Command{
		Use:   "plan",
		Short: "Plan one-home v1-to-v2 task authority migration without source mutation",
		Args:  contractNoArgs,
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			if _, err := contractOutput(cmd); err != nil {
				return err
			}
			planPath, _ := cmd.Flags().GetString("plan-out")
			if planPath == "" {
				return usageError("invalid_argument", "Run `munsu migrate task-authority plan --plan-out <plan.json>`", "--plan-out is required")
			}
			plan, err := taskauthorityfs.PlanMigration(ctx.Home)
			if err != nil {
				if errors.Is(err, taskauthorityfs.ErrAlreadyMigrated) {
					return writeContract(cmd, Response[MessageResult]{
						SchemaVersion: SchemaVersion,
						Kind:          "migrate.task_authority.plan.already_migrated",
						Status:        "success",
						Data:          MessageResult{Message: "Task authority is already migrated to v2; nothing to plan"},
					})
				}
				return operationError("invalid_argument", "Run `munsu migrate task-authority plan --plan-out <plan.json>`", err.Error())
			}
			if err := taskauthorityfs.WriteMigrationPlan(planPath, plan); err != nil {
				return operationError("internal", "Run the task-authority plan command again", err.Error())
			}
			targets := len(plan.Aggregates) + len(plan.Holds) + len(plan.Interpretations) + len(plan.Decisions)
			message := fmt.Sprintf("Planned %d v1 record(s) into %d v2 target(s), quarantined=%d; digest=%s; plan=%s; apply=munsu migrate task-authority apply --plan %s", plan.RecordCount, targets, len(plan.Quarantined), plan.SourceDigest, planPath, shellQuote(planPath))
			return writeContract(cmd, Response[MessageResult]{
				SchemaVersion: SchemaVersion,
				Kind:          "migrate.task_authority.plan",
				Status:        "success",
				Data:          MessageResult{Message: message},
			})
		}),
	}
	planCmd.Flags().String("plan-out", "", "Path to write reviewed task-authority migration plan JSON")
	configureContractCommand(planCmd)
	apply := &cobra.Command{
		Use:   "apply",
		Short: "Apply one reviewed v1-to-v2 task authority migration plan",
		Args:  contractNoArgs,
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			if _, err := contractOutput(cmd); err != nil {
				return err
			}
			planPath, _ := cmd.Flags().GetString("plan")
			if planPath == "" {
				return usageError("invalid_argument", "Run `munsu migrate task-authority plan` first, then `munsu migrate task-authority apply --plan <plan.json>`", "--plan is required")
			}
			plan, err := taskauthorityfs.ReadMigrationPlan(planPath)
			if err != nil {
				return operationError("invalid_argument", "Run `munsu migrate task-authority plan` again", err.Error())
			}
			receipt, err := taskauthorityfs.ApplyMigration(plan)
			if err != nil {
				if errors.Is(err, taskauthorityfs.ErrAlreadyMigrated) {
					return writeContract(cmd, Response[MessageResult]{
						SchemaVersion: SchemaVersion,
						Kind:          "migrate.task_authority.apply.already_migrated",
						Status:        "success",
						Data:          MessageResult{Message: "Task authority is already migrated to v2; receipt verified, nothing rewritten"},
					})
				}
				return operationError("internal", "Re-run the same `munsu migrate task-authority apply --plan <plan.json>` after fixing the reported state", err.Error())
			}
			message := fmt.Sprintf("Migrated %d v1 record(s) to v2; digest=%s; archive=%s", receipt.RecordCount, receipt.SourceDigest, receipt.ArchivePath)
			return writeContract(cmd, Response[MessageResult]{
				SchemaVersion: SchemaVersion,
				Kind:          "migrate.task_authority.apply",
				Status:        "success",
				Data:          MessageResult{Message: message},
			})
		}),
	}
	apply.Flags().String("plan", "", "Path to reviewed task-authority migration plan JSON")
	configureContractCommand(apply)
	cmd.AddCommand(planCmd)
	cmd.AddCommand(apply)
	return cmd
}

func newMigrateConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Migrate legacy config files to typed documents",
		Long: `Migrate legacy captains.md, projects.md, and soldier-dispatch.json
files to the typed document system (config/base.json, data/captains.json,
data/projects.json). Legacy files are archived and migration receipts are
produced.`,
	}
	planCmd := &cobra.Command{
		Use:   "plan",
		Short: "Plan config migration without source mutation",
		Args:  contractNoArgs,
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			if _, err := contractOutput(cmd); err != nil {
				return err
			}
			planPath, _ := cmd.Flags().GetString("plan-out")
			if planPath == "" {
				return usageError("invalid_argument", "Run `munsu migrate config plan --plan-out <plan.json>`", "--plan-out is required")
			}
			plan, err := configmigration.PlanConfigMigration(ctx.Home)
			if err != nil {
				return operationError("invalid_argument", configmigration.MigrationCommand(ctx.Home), err.Error())
			}
			data, err := json.MarshalIndent(plan, "", "  ")
			if err != nil {
				return operationError("internal", configmigration.MigrationCommand(ctx.Home), err.Error())
			}
			if err := os.WriteFile(planPath, append(data, '\n'), 0600); err != nil {
				return operationError("internal", configmigration.MigrationCommand(ctx.Home), err.Error())
			}
			message := fmt.Sprintf("Planned %d legacy config file(s); digest=%s; plan=%s; apply=%s",
				len(plan.LegacyFiles), plan.PlanDigest, planPath, configmigration.MigrationCommand(ctx.Home))
			return writeContract(cmd, Response[MessageResult]{
				SchemaVersion: SchemaVersion,
				Kind:          "migrate.config.plan",
				Status:        "success",
				Data:          MessageResult{Message: message},
			})
		}),
	}
	planCmd.Flags().String("plan-out", "", "Path to write reviewed config migration plan JSON")
	configureContractCommand(planCmd)
	apply := &cobra.Command{
		Use:   "apply",
		Short: "Apply one reviewed config migration plan",
		Args:  contractNoArgs,
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			if _, err := contractOutput(cmd); err != nil {
				return err
			}
			planPath, _ := cmd.Flags().GetString("plan")
			if planPath == "" {
				return usageError("invalid_argument", "Run `munsu migrate config plan` first, then `munsu migrate config apply --plan <plan.json>`", "--plan is required")
			}
			data, err := os.ReadFile(planPath)
			if err != nil {
				return operationError("invalid_argument", "Run `munsu migrate config plan` again", err.Error())
			}
			var plan configmigration.ConfigMigrationPlan
			if err := json.Unmarshal(data, &plan); err != nil {
				return operationError("invalid_argument", "Run `munsu migrate config plan` again", err.Error())
			}
			receipt, err := configmigration.ApplyConfigMigration(&plan)
			if err != nil {
				return operationError("internal", "Re-run the same `munsu migrate config apply --plan <plan.json>` after fixing the reported state", err.Error())
			}
			message := fmt.Sprintf("Migrated %d legacy config file(s); archive=%s", len(receipt.LegacyFiles), receipt.ArchivePath)
			return writeContract(cmd, Response[MessageResult]{
				SchemaVersion: SchemaVersion,
				Kind:          "migrate.config.apply",
				Status:        "success",
				Data:          MessageResult{Message: message},
			})
		}),
	}
	apply.Flags().String("plan", "", "Path to reviewed config migration plan JSON")
	configureContractCommand(apply)
	cmd.AddCommand(planCmd)
	cmd.AddCommand(apply)
	return cmd
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
