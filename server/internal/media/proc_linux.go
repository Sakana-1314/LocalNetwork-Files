package media

import (
	"os/exec"
	"syscall"
)

// setProcAttr makes the kernel kill ffmpeg when the server process dies, so a
// crashed or force-killed server does not leave transcoders running.
func setProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
}
