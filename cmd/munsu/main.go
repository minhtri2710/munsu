package main

import (
	"fmt"
	"os"

	"github.com/minhtri2710/munsu/internal/cli"
)

func main() {
	root := cli.NewRootCommand()
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
