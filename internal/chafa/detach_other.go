//go:build !unix

package chafa

import "os/exec"

// detached is a no-op without Unix sessions; --probe off (chafa 1.16+)
// still keeps chafa from querying the terminal.
func detached(cmd *exec.Cmd) *exec.Cmd { return cmd }
