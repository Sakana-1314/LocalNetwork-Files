//go:build !linux

package smb

import "syscall"

func sysProcAttr() *syscall.SysProcAttr { return nil }
