package daemon

import (
	"fmt"
	"os"
	"strconv"
	"syscall"
)

// WritePID atomically writes the given PID to the file at path.
func WritePID(path string, pid int) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(fmt.Sprintf("%d\n", pid)), 0o644); err != nil {
		return fmt.Errorf("write pid file: %w", err)
	}
	return os.Rename(tmp, path)
}

// ReadPID reads and returns the PID from the file at path.
// Returns an error if the file is missing or the content is malformed.
func ReadPID(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("read pid file: %w", err)
	}
	pid, err := strconv.Atoi(string(data[:len(data)]))
	// Trim trailing newline if present.
	if err != nil {
		trimmed := string(data)
		for len(trimmed) > 0 && (trimmed[len(trimmed)-1] == '\n' || trimmed[len(trimmed)-1] == '\r') {
			trimmed = trimmed[:len(trimmed)-1]
		}
		pid, err = strconv.Atoi(trimmed)
		if err != nil {
			return 0, fmt.Errorf("parse pid %q: %w", string(data), err)
		}
	}
	return pid, nil
}

// RemovePID removes the PID file at path.
func RemovePID(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove pid file: %w", err)
	}
	return nil
}

// IsRunning checks whether a process with the given PID is alive.
func IsRunning(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}
