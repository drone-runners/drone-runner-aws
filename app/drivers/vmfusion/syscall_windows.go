//go:build windows
// +build windows

package vmfusion

func syscallUmask() {
	// syscall umask is not supported on Windows
}
