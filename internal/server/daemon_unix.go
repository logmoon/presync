//go:build !windows

package server

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func configureDaemonCmd(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}
}

func sendStopSignal(process *os.Process) error {
	return process.Signal(syscall.SIGTERM)
}

func processAlive(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = process.Signal(syscall.Signal(0))
	return err == nil
}

func pidUptime(pid int) (time.Duration, error) {
	statPath := fmt.Sprintf("/proc/%d/stat", pid)
	data, err := os.ReadFile(statPath)
	if err != nil {
		return 0, err
	}

	content := string(data)
	// Find the closing parenthesis to skip the process name (which may contain spaces)
	endIdx := -1
	for i := len(content) - 1; i >= 0; i-- {
		if content[i] == ')' {
			endIdx = i
			break
		}
	}
	if endIdx == -1 || endIdx+2 >= len(content) {
		return 0, fmt.Errorf("could not parse stat file")
	}

	// Fields after the process name: state ppid pgrp session tty_nr tpgid flags minflt cminflt majflt cmajflt utime stime cutime cstime priority nice num_threads itrealvalue starttime
	// starttime is field index 19 (0-based) after the closing paren
	fields := strings.Fields(content[endIdx+2:])
	if len(fields) < 20 {
		return 0, fmt.Errorf("not enough fields in stat file")
	}

	starttime, err := strconv.ParseInt(fields[19], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parsing starttime: %w", err)
	}

	clkTck := int64(100) // standard Linux clock tick rate (HZ)

	uptimeRaw, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0, err
	}

	var uptime float64
	fmt.Sscanf(string(uptimeRaw), "%f", &uptime)

	startSecs := float64(starttime) / float64(clkTck)
	elapsed := uptime - startSecs
	if elapsed < 0 {
		elapsed = 0
	}

	return time.Duration(elapsed * float64(time.Second)), nil
}

func tailFile(path string) error {
	cmd := exec.Command("tail", "-f", path)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
