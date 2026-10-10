package smb

import "syscall"

// sysProcAttr makes the kernel terminate smbd if the server dies without
// stopping it (e.g. SIGKILL), so no orphan keeps the SMB port bound.
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
}
