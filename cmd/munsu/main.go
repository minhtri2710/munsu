package main

import (
	"os"

	"github.com/minhtri2710/munsu/internal/cli"
)

func main() {
	root := cli.NewRootCommand()
	if err := root.Execute(); err != nil {
		exitCode := cli.WriteContractError(os.Stdout, err, os.Args[1:])
		if exitCode == 0 {
			exitCode = 1
		}
		os.Exit(exitCode)
	}
}
