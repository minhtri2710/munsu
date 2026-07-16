package main

import (
	"fmt"
	"os"

	"github.com/minhtri2710/munsu/internal/cli"
)

func main() {
	root := cli.NewRootCommand()
	if err := root.Execute(); err != nil {
		if exitCode := cli.WriteContractError(os.Stdout, err, os.Args[1:]); exitCode != 0 {
			os.Exit(exitCode)
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
