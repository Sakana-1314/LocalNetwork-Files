//go:build !linux

package media

import "os/exec"

func setProcAttr(cmd *exec.Cmd) {}
