package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteReadPID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.pid")

	want := os.Getpid()

	if err := WritePID(path, want); err != nil {
		t.Fatalf("WritePID: %v", err)
	}

	got, err := ReadPID(path)
	if err != nil {
		t.Fatalf("ReadPID: %v", err)
	}
	if got != want {
		t.Errorf("ReadPID = %d, want %d", got, want)
	}
}

func TestReadPIDMissing(t *testing.T) {
	_, err := ReadPID("/nonexistent/path/daemon.pid")
	if err == nil {
		t.Fatal("ReadPID on missing file: expected error, got nil")
	}
}

func TestRemovePID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.pid")

	if err := WritePID(path, 12345); err != nil {
		t.Fatalf("WritePID: %v", err)
	}

	if err := RemovePID(path); err != nil {
		t.Fatalf("RemovePID: %v", err)
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("PID file still exists after RemovePID")
	}
}

func TestRemovePIDMissing(t *testing.T) {
	// Removing a nonexistent PID file should not error.
	if err := RemovePID("/nonexistent/path/daemon.pid"); err != nil {
		t.Fatalf("RemovePID on missing file: %v", err)
	}
}

func TestIsRunning(t *testing.T) {
	// Own process should be running.
	if !IsRunning(os.Getpid()) {
		t.Error("IsRunning(own PID) = false, want true")
	}

	// PID 99999 should not be running (reserved, not in use on most systems).
	if IsRunning(99999) {
		t.Error("IsRunning(99999) = true, want false")
	}
}
