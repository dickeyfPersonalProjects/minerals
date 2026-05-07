// Command minerals is the v1 backend binary. It dispatches on its
// first positional argument: `serve` (default), `migrate`, or
// `openapi`. All subcommands share the same image; the operator
// picks one via CMD or args. `openapi` is a build-time helper —
// it writes the type-derived spec to stdout for the frontend
// codegen pipeline (per CONTRACT.md §10 / mi-cy4).
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
	case "openapi":
		err = runOpenAPI(args)
	default:
		err = fmt.Errorf("unknown subcommand %q (want: serve | migrate | openapi)", cmd)
	}
	if err != nil {
		slog.Error("command failed", "cmd", cmd, "err", err)
		os.Exit(1)
	}
}
