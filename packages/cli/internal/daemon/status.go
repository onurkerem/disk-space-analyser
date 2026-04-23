package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// WriteStatus atomically writes the given Status to path.
// It writes to a temporary file next to the target and renames it into place,
// preventing partial reads by concurrent readers.
func WriteStatus(path string, status Status) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create status dir: %w", err)
	}

	data, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal status: %w", err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write status temp file: %w", err)
	}

	if err := os.Rename(tmp, path); err != nil {
		// Clean up the temp file if rename fails.
		_ = os.Remove(tmp)
		return fmt.Errorf("rename status file: %w", err)
	}

	return nil
}

// ReadStatus reads and unmarshals the status file at path.
// Returns a zero-value Status with no error if the file does not exist
// (daemon has never run yet).
func ReadStatus(path string) (Status, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Status{}, nil
		}
		return Status{}, fmt.Errorf("read status file: %w", err)
	}

	var status Status
	if err := json.Unmarshal(data, &status); err != nil {
		return Status{}, fmt.Errorf("unmarshal status: %w", err)
	}

	return status, nil
}

// RemoveStatus removes the status file at path. Returns nil if it doesn't exist.
func RemoveStatus(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove status file: %w", err)
	}
	return nil
}
