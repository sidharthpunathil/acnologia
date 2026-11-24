// Command acn is the Acnologia command line client: sign in to a server
// through the browser, then manage its services, keys and apps.
//
//	go install github.com/sidharthpunathil/acnologia/server/cmd/acn@latest
//	acn login
//
// It is the same client as `acnologia ctl`, without the server, so it stays
// small and depends only on the standard library.
package main

import (
	"fmt"
	"os"
	"runtime/debug"
	"slices"

	"github.com/sidharthpunathil/acnologia/server/internal/ctl"
)

// version is set at build time (-X main.version=...); go install builds
// fall back to the module version.
var version = "dev"

func main() {
	v := version
	if v == "dev" {
		if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			v = bi.Main.Version
		}
	}
	// acn version --server asks the server instead (ctl/safety.go).
	if len(os.Args) > 1 && (os.Args[1] == "version" || os.Args[1] == "--version") && !slices.Contains(os.Args[2:], "--server") {
		fmt.Println("acn", v)
		return
	}
	// The server's audit log records the client as acn/<version>.
	ctl.Version = v
	os.Exit(ctl.RunAs("acn", os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
