// Command gote is a simple TUI text editor built on the bubblestack framework. The bare
// invocation launches the editor; the `update` subcommand self-updates the binary from
// the latest GitHub release (see cmd/).
package main

import "github.com/brohd11/gote/cmd"

func main() {
	cmd.Execute()
}
