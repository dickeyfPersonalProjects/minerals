// Command minerals is the v1 backend binary. It dispatches on its
// first positional argument: `serve` (default) or `migrate`. Both
// subcommands share the same image; the operator picks one via CMD
// or args.
package main

import (
	"fmt"
	"log/slog"
	"os"
)

// version is baked at build time via -ldflags="-X main.version=...".
var version = "dev"

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	args := os.Args[1:]
	cmd := "serve"
	if len(args) > 0 {
		cmd = args[0]
		args = args[1:]
	}

	var err error
	switch cmd {
	case "serve":
		err = runServe(args)
	case "migrate":
		err = runMigrate(args)
	default:
		err = fmt.Errorf("unknown subcommand %q (want: serve | migrate)", cmd)
	}
	if err != nil {
		slog.Error("command failed", "cmd", cmd, "err", err)
		os.Exit(1)
	}
}
