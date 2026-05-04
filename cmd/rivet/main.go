package main

import (
	"fmt"
	"os"
	"runtime/debug"

	"github.com/djtouchette/rivet/internal/cli"
)

// version is overridden at build time via -ldflags. When unset, we fall back
// to the module version baked in by `go install ...@vX.Y.Z`.
var version = "dev"

func main() {
	root := cli.NewRootCmd(resolveVersion())
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func resolveVersion() string {
	if version != "dev" {
		return version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" || info.Main.Version == "(devel)" {
		return version
	}
	return info.Main.Version
}
