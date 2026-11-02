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

	"github.com/sidharthpunathil/acnologia/server/internal/ctl"
)

// version is set at build time (-X main.version=...); go install builds
// fall back to the module version.
var version = "dev"

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "version" || os.Args[1] == "--version") {
		v := version
		if v == "dev" {
			if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
				v = bi.Main.Version
			}
		}
		fmt.Println("acn", v)
		return
	}
	os.Exit(ctl.RunAs("acn", os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
