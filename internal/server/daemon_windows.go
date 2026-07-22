//go:build windows

package server

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
	"unsafe"
)

const detachProcess = 0x00000008

func configureDaemonCmd(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: detachProcess,
	}
}

func sendStopSignal(process *os.Process) error {
	return process.Kill()
}

func processAlive(pid int) bool {
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

func pidUptime(pid int) (time.Duration, error) {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	procOpenProcess := kernel32.NewProc("OpenProcess")

	handle, _, _ := procOpenProcess.Call(
		uintptr(0x1000), // PROCESS_QUERY_LIMITED_INFORMATION
		uintptr(0),
		uintptr(pid),
	)
	if handle == 0 {
		return 0, fmt.Errorf("could not open process %d", pid)
	}
	defer syscall.CloseHandle(syscall.Handle(handle))

	procGetProcessTimes := kernel32.NewProc("GetProcessTimes")
	var creation, exit, kernel, user syscall.Filetime
	ret, _, _ := procGetProcessTimes.Call(
		handle,
		uintptr(unsafe.Pointer(&creation)),
		uintptr(unsafe.Pointer(&exit)),
		uintptr(unsafe.Pointer(&kernel)),
		uintptr(unsafe.Pointer(&user)),
	)
	if ret == 0 {
		return 0, fmt.Errorf("GetProcessTimes failed")
	}

	nsec := int64(creation.HighDateTime)<<32 + int64(creation.LowDateTime)
	nsec -= 116444736000000000             // offset between Unix epoch and Windows epoch
	creationTime := time.Unix(0, nsec*100) // FILETIME is in 100-nanosecond intervals
	return time.Since(creationTime), nil
}

func tailFile(path string) error {
	cmd := exec.Command("powershell", "-Command", "Get-Content", "-Wait", "-Path", path)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
