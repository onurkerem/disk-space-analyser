package daemon

import (
	"os"
	"path/filepath"
)

const appDirName = ".disk-space-analyser"

// DataDir returns the application data directory, creating it if needed.
func DataDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, appDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// PIDPath returns the path to the daemon PID file.
func PIDPath() (string, error) {
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "daemon.pid"), nil
}

// StatusPath returns the path to the daemon status file.
func StatusPath() (string, error) {
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "status.json"), nil
}

// LogPath returns the path to the daemon log file.
func LogPath() (string, error) {
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "daemon.log"), nil
}

// DBPath returns the path to the SQLite database file.
func DBPath() (string, error) {
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "data.db"), nil
}

// Status represents the runtime state of the daemon, written to status.json.
type Status struct {
	PID         int64  `json:"pid"`
	Running     bool   `json:"running"`
	Scanning    bool   `json:"scanning,omitempty"`
	RootPath    string `json:"root_path"`
	ScannedDirs int64  `json:"scanned_dirs"`
	ScannedAt   string `json:"scanned_at"`
	Error       string `json:"error,omitempty"`
}
