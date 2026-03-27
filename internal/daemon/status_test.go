package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteReadStatus(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "status.json")

	want := Status{
		PID:        12345,
		Running:    true,
		RootPath:   "/tmp/test",
		ScannedDirs: 42,
		ScannedAt:  "2026-03-27T10:00:00Z",
		Error:      "",
	}

	if err := WriteStatus(path, want); err != nil {
		t.Fatalf("WriteStatus: %v", err)
	}

	got, err := ReadStatus(path)
	if err != nil {
		t.Fatalf("ReadStatus: %v", err)
	}

	if got != want {
		t.Errorf("ReadStatus = %+v, want %+v", got, want)
	}
}

func TestReadStatusMissing(t *testing.T) {
	got, err := ReadStatus("/nonexistent/path/status.json")
	if err != nil {
		t.Fatalf("ReadStatus on missing file: unexpected error: %v", err)
	}
	if got != (Status{}) {
		t.Errorf("ReadStatus on missing file = %+v, want zero-value Status", got)
	}
}

func TestWriteStatusAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "status.json")

	status := Status{
		PID:        1,
		Running:    true,
		RootPath:   "/",
		ScannedDirs: 10,
		ScannedAt:  "2026-01-01T00:00:00Z",
	}

	if err := WriteStatus(path, status); err != nil {
		t.Fatalf("WriteStatus: %v", err)
	}

	// The .tmp file should not remain after a successful write.
	tmpPath := path + ".tmp"
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Errorf("temp file %s still exists after WriteStatus", tmpPath)
	}

	// The real file should exist and be readable.
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("status file does not exist after WriteStatus")
	}
}
