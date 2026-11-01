//go:build windows

package client

import (
	"os"
	"os/exec"
	"syscall"
	"unsafe"
)

const detachWatcherProcess = 0x00000008

func configureWatcherCmd(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: detachWatcherProcess,
	}
}

func sendWatcherStop(process *os.Process) error {
	return process.Kill()
}

func watcherProcessAlive(pid int) bool {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	procOpenProcess := kernel32.NewProc("OpenProcess")

	handle, _, _ := procOpenProcess.Call(
		uintptr(0x1000), // PROCESS_QUERY_LIMITED_INFORMATION
		uintptr(0),
		uintptr(pid),
	)
	if handle == 0 {
		return false
	}
	defer syscall.CloseHandle(syscall.Handle(handle))

	var exitCode uint32
	procGetExitCodeProcess := kernel32.NewProc("GetExitCodeProcess")
	ret, _, _ := procGetExitCodeProcess.Call(
		handle,
		uintptr(unsafe.Pointer(&exitCode)),
	)
	if ret == 0 {
		return false
	}

	return exitCode == 259 // STILL_ACTIVE
}
