//go:build ignore

package main

import (
	"github.com/launchrctl/launchr"

	_ "github.com/skilld-labs/plasmactl-update"
)

func main() {
	launchr.GenAndExit()
}
