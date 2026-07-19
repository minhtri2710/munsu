package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/minhtri2710/munsu/internal/contract"
	"github.com/spf13/cobra"
)

func newSkillCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skill",
		Short: "Inspect embedded munsu skills",
		Long: `Inspect the munsu skills embedded in the binary.

munsu ships the munsu-ops skill on disk (installed by 'munsu init') plus several
auxiliary skills that are read on demand via this command. munsu-ops points to
them with "Context pointer" notes telling you which skill to read for each situation.`,
	}
	cmd.AddCommand(newSkillListCmd())
	cmd.AddCommand(newSkillShowCmd())
	return cmd
}

func newSkillListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List embedded skills",
		RunE: func(cmd *cobra.Command, args []string) error {
			names, err := embeddedSkillNames()
			if err != nil {
				return err
			}
			sort.Strings(names)
			return writeContract(cmd, contract.Response[contract.MessageResult]{
				SchemaVersion: contract.SchemaVersion,
				Kind:          "skill.list",
				Status:        "success",
				Data:          contract.MessageResult{Message: strings.Join(names, "\n")},
			})
		},
	}
	configureContractCommand(cmd)
	return cmd
}

func newSkillShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <name>",
		Short: "Print the contents of an embedded skill",
		Long: `Print the full contents of an embedded skill to stdout.

Read this output as the skill body when munsu-ops points you to one of the
auxiliary skills (bootstrap-diagnostics, harness-adapters, munsu-update,
captain-provisioning, stuck-soldier-recovery).`,
		Args: ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			content, err := readEmbeddedSkill(args[0])
			if err != nil {
				return err
			}
			fmt.Print(content)
			return nil
		},
	}
}
