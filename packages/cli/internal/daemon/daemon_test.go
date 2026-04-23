package daemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestDataDir(t *testing.T) {
	// DataDir creates ~/.disk-space-analyser. We can't change HOME here without
	// affecting the real system, so we just verify it returns a non-empty path
	// and the directory exists.
	dir, err := DataDir()
	if err != nil {
		t.Fatalf("DataDir: %v", err)
	}
	if dir == "" {
		t.Fatal("DataDir returned empty path")
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat %s: %v", dir, err)
	}
	if !info.IsDir() {
		t.Errorf("DataDir path %s is not a directory", dir)
	}
}

func TestPaths(t *testing.T) {
	dir, err := DataDir()
	if err != nil {
		t.Fatalf("DataDir: %v", err)
	}

	tests := []struct {
		name    string
		fn      func() (string, error)
		wantSuffix string
	}{
		{"PIDPath", PIDPath, "daemon.pid"},
		{"StatusPath", StatusPath, "status.json"},
		{"LogPath", LogPath, "daemon.log"},
		{"DBPath", DBPath, "data.db"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.fn()
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			expected := filepath.Join(dir, tc.wantSuffix)
			if got != expected {
				t.Errorf("%s = %q, want %q", tc.name, got, expected)
			}
		})
	}
}

func TestLifecycleRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping lifecycle test in short mode")
	}

	// Create a small temp directory to scan.
	scanRoot := t.TempDir()
	homeDir := t.TempDir()
	for i := 0; i < 5; i++ {
		sub := filepath.Join(scanRoot, "dir"+string(rune('A'+i)))
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		for j := 0; j < 3; j++ {
			f, err := os.CreateTemp(sub, "file-*.txt")
			if err != nil {
				t.Fatal(err)
			}
			f.WriteString("test data for scanning\n")
			f.Close()
		}
	}

	// Create a small temp directory to scan.
	dataDir := filepath.Join(homeDir, ".disk-space-analyser")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// We can't override HOME for the daemon since it uses os.UserHomeDir().
	// Instead, we test the lifecycle by directly exercising daemon functions.

	// 1. Write a PID file for our own process (simulating daemon start).
	pidPath := filepath.Join(dataDir, "daemon.pid")
	statusPath := filepath.Join(dataDir, "status.json")
	ownPID := os.Getpid()

	if err := WritePID(pidPath, ownPID); err != nil {
		t.Fatalf("WritePID: %v", err)
	}

	// Verify PID was written.
	readPID, err := ReadPID(pidPath)
	if err != nil {
		t.Fatalf("ReadPID: %v", err)
	}
	if readPID != ownPID {
		t.Fatalf("ReadPID = %d, want %d", readPID, ownPID)
	}

	// Verify process is running.
	if !IsRunning(ownPID) {
		t.Fatal("IsRunning(ownPID) = false")
	}

	// 2. Write status as if daemon is running.
	status := Status{
		PID:        int64(ownPID),
		Running:    true,
		RootPath:   scanRoot,
		ScannedDirs: 5,
		ScannedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	if err := WriteStatus(statusPath, status); err != nil {
		t.Fatalf("WriteStatus: %v", err)
	}

	// 3. Read status back and verify.
	readStatus, err := ReadStatus(statusPath)
	if err != nil {
		t.Fatalf("ReadStatus: %v", err)
	}
	if readStatus.PID != int64(ownPID) {
		t.Errorf("status.PID = %d, want %d", readStatus.PID, ownPID)
	}
	if !readStatus.Running {
		t.Error("status.Running = false, want true")
	}
	if readStatus.RootPath != scanRoot {
		t.Errorf("status.RootPath = %q, want %q", readStatus.RootPath, scanRoot)
	}

	// 4. Simulate stop: send SIGTERM to self... but that would kill the test.
	// Instead, fork a subprocess, write its PID, then kill it.

	// Start a simple subprocess that will just sleep.
	sleepCmd := exec.Command("sleep", "30")
	sleepCmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := sleepCmd.Start(); err != nil {
		t.Fatalf("start sleep process: %v", err)
	}
	childPID := sleepCmd.Process.Pid

	// Write child PID.
	if err := WritePID(pidPath, childPID); err != nil {
		t.Fatalf("WritePID(child): %v", err)
	}

	// Verify child is running.
	if !IsRunning(childPID) {
		t.Fatal("child process not running after start")
	}

	// Send SIGTERM to child.
	if err := syscall.Kill(childPID, syscall.SIGTERM); err != nil {
		t.Fatalf("SIGTERM child: %v", err)
	}

	// Wait for child to exit.
	done := make(chan error, 1)
	go func() {
		done <- sleepCmd.Wait()
	}()

	select {
	case err := <-done:
		if err != nil {
			// SIGTERM causes exit status, that's fine.
			t.Logf("sleep process exited: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for child to exit after SIGTERM")
	}

	// Verify child is no longer running.
	if IsRunning(childPID) {
		t.Error("child process still running after SIGTERM")
	}

	// Remove PID file.
	if err := RemovePID(pidPath); err != nil {
		t.Fatalf("RemovePID: %v", err)
	}

	// Verify PID file is gone.
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Error("PID file still exists after RemovePID")
	}
}
