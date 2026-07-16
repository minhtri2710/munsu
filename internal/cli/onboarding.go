package cli

import "fmt"

// printNextSteps prints a short block of recommended commands after init completes.
// Extracted into a testable helper so the output can be verified.
func printNextSteps(homeDir string) {
	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Println()
	fmt.Println("  munsu home --mkdir                     Create home directory (if not done)")
	fmt.Println("  munsu config get backend               Check detected backend")
	fmt.Println("  munsu config get crew-harness          Check detected crew harness")
	fmt.Println("  munsu doctor                            Run diagnostics")
	fmt.Println("  munsu project add <name> <path-or-url>  Register a project")
	fmt.Println("  munsu session-start                     Start a session")
	fmt.Println("  munsu --help                            View all commands")
	fmt.Println()
	fmt.Printf("Home:  %s\n", homeDir)
}
