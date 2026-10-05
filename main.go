// Command doppel manages several Git accounts on one machine: a commit
// identity, SSH keys and the folders where each account applies.
package main

import (
	"os"

	"github.com/vehkiya/doppel/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:]))
}
