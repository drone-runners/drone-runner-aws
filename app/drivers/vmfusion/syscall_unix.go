//go:build !windows
// +build !windows

package vmfusion

import "syscall"

func syscallUmask() {
	_ = syscall.Umask(022) //nolint
}
