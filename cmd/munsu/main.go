package main

import (
	"os"

	"github.com/minhtri2710/munsu/internal/cli"
)

func main() {
	if code, ok := cli.RunCaptainSeatRequest(os.Args[1:], os.Stderr); ok {
		os.Exit(code)
	}
	root := cli.NewRootCommand()
	if err := root.Execute(); err != nil {
		exitCode := cli.WriteContractError(os.Stdout, err, os.Args[1:])
		if exitCode == 0 {
			exitCode = 1
		}
		os.Exit(exitCode)
	}
}
