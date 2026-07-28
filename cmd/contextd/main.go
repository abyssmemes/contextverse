package main

import (
	"fmt"
	"os"

	"github.com/orkcom-tech/contextverse/internal/cli"
	"github.com/orkcom-tech/contextverse/internal/logx"
)

func main() {
	if err := cli.Execute(); err != nil {
		code := cli.ExitCodeFor(err)
		logx.L().Error("command failed", "err", err, "exit_code", code)
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(code)
	}
}
