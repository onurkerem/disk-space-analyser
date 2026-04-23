package scanner

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"disk-space-analyser/internal/db"
)

func TestSizeThresholdSmallDir(t *testing.T) {
	// A directory under the threshold should be treated as shallow.
	root := t.TempDir()
	database, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	structure := map[string]int64{
		"small/a/file1.txt": 100,
		"small/b/file2.txt": 200,
		"small/c/file3.txt": 300,
	}
	createTestTree(t, root, structure)

	scanner := New(database, Config{
		Concurrency:       4,
		BatchSize:         100,
		ChannelSize:       100,
		SmallDirThreshold: 1024, // 1 KB threshold
	})

	ctx := context.Background()
	if err := scanner.Scan(ctx, root); err != nil {
		t.Fatalf("scan: %v", err)
	}

	// small/ should be shallow (under 1 KB threshold).
	smallPath := filepath.Join(root, "small")
	smallEntry, err := database.GetDir(ctx, smallPath)
	if err != nil {
		t.Fatal(err)
	}
	if smallEntry == nil {
		t.Fatal("small/ entry not found in DB")
	}
	if !smallEntry.Shallow {
		t.Error("small/ should be marked as shallow (under threshold)")
	}
	if smallEntry.Size != 600 {
		t.Errorf("small/ size: expected 600, got %d", smallEntry.Size)
	}

	// small/ should have NO children in DB.
	children, err := database.GetChildren(ctx, smallPath, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(children) != 0 {
		t.Errorf("small/ should have no children, got %d", len(children))
	}

	// Root should include small/'s size.
	rootEntry, err := database.GetDir(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if rootEntry.Size != 600 {
		t.Errorf("root size: expected 600, got %d", rootEntry.Size)
	}

	// Total entries: root + small/ = 2
	count, err := database.CountDirs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Errorf("expected 2 entries, got %d", count)
	}
}

func TestSizeThresholdLargeDir(t *testing.T) {
	// A directory over the threshold should be scanned deeply.
	root := t.TempDir()
	database, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	structure := map[string]int64{
		"big/a/file1.txt": 500,
		"big/b/file2.txt": 600,
	}
	createTestTree(t, root, structure)

	scanner := New(database, Config{
		Concurrency:       4,
		BatchSize:         100,
		ChannelSize:       100,
		SmallDirThreshold: 1000, // 1 KB threshold — big/ is 1100 bytes, over threshold
	})

	ctx := context.Background()
	if err := scanner.Scan(ctx, root); err != nil {
		t.Fatalf("scan: %v", err)
	}

	// big/ should NOT be shallow (over threshold).
	bigPath := filepath.Join(root, "big")
	bigEntry, err := database.GetDir(ctx, bigPath)
	if err != nil {
		t.Fatal(err)
	}
	if bigEntry == nil {
		t.Fatal("big/ entry not found in DB")
	}
	if bigEntry.Shallow {
		t.Error("big/ should NOT be shallow (over threshold)")
	}

	// big/ should have children (a/, b/) in DB.
	children, err := database.GetChildren(ctx, bigPath, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(children) != 2 {
		t.Errorf("big/ should have 2 children, got %d", len(children))
	}

	// Root should include total size.
	rootEntry, err := database.GetDir(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if rootEntry.Size != 1100 {
		t.Errorf("root size: expected 1100, got %d", rootEntry.Size)
	}
}

func TestSizeThresholdMixed(t *testing.T) {
	// Parent with both small and large subdirs.
	root := t.TempDir()
	database, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	structure := map[string]int64{
		"small/a/file1.txt": 100,
		"small/b/file2.txt": 200,
		"big/c/file3.txt":   500,
		"big/d/file4.txt":   600,
	}
	createTestTree(t, root, structure)

	scanner := New(database, Config{
		Concurrency:       4,
		BatchSize:         100,
		ChannelSize:       100,
		SmallDirThreshold: 500, // 500 bytes — small/ is 300 (under), big/ is 1100 (over)
	})

	ctx := context.Background()
	if err := scanner.Scan(ctx, root); err != nil {
		t.Fatalf("scan: %v", err)
	}

	// small/ should be shallow.
	smallPath := filepath.Join(root, "small")
	smallEntry, err := database.GetDir(ctx, smallPath)
	if err != nil {
		t.Fatal(err)
	}
	if !smallEntry.Shallow {
		t.Error("small/ should be shallow")
	}
	if smallEntry.Size != 300 {
		t.Errorf("small/ size: expected 300, got %d", smallEntry.Size)
	}

	// big/ should be deep.
	bigPath := filepath.Join(root, "big")
	bigEntry, err := database.GetDir(ctx, bigPath)
	if err != nil {
		t.Fatal(err)
	}
	if bigEntry.Shallow {
		t.Error("big/ should NOT be shallow")
	}

	// Root total = 300 + 1100 = 1400.
	rootEntry, err := database.GetDir(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if rootEntry.Size != 1400 {
		t.Errorf("root size: expected 1400, got %d", rootEntry.Size)
	}

	// Entries: root, small/ (shallow), big/, c/, d/ = 5
	count, err := database.CountDirs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if count != 5 {
		t.Errorf("expected 5 entries, got %d", count)
	}
}

func TestSizeThresholdDisabled(t *testing.T) {
	// Threshold = 0 should disable size pruning entirely.
	root := t.TempDir()
	database, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	structure := map[string]int64{
		"tiny/a/file1.txt": 10,
	}
	createTestTree(t, root, structure)

	scanner := New(database, Config{
		Concurrency:       4,
		BatchSize:         100,
		ChannelSize:       100,
		SmallDirThreshold: 0, // disabled
	})

	ctx := context.Background()
	if err := scanner.Scan(ctx, root); err != nil {
		t.Fatalf("scan: %v", err)
	}

	// tiny/ should NOT be shallow — threshold disabled.
	tinyPath := filepath.Join(root, "tiny")
	tinyEntry, err := database.GetDir(ctx, tinyPath)
	if err != nil {
		t.Fatal(err)
	}
	if tinyEntry == nil {
		t.Fatal("tiny/ entry not found")
	}
	if tinyEntry.Shallow {
		t.Error("tiny/ should NOT be shallow when threshold is disabled")
	}

	// All levels should be persisted: root, tiny/, a/
	count, err := database.CountDirs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Errorf("expected 3 entries (root, tiny, a), got %d", count)
	}
}

func TestSizeThresholdWithShallowPattern(t *testing.T) {
	// Pattern-based shallow should still take priority over size-based.
	root := t.TempDir()
	database, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	structure := map[string]int64{
		"node_modules/pkg/file.txt": 100,
	}
	createTestTree(t, root, structure)

	scanner := New(database, Config{
		Concurrency:       4,
		BatchSize:         100,
		ChannelSize:       100,
		SmallDirThreshold: 1024, // node_modules (100 bytes) would also be under this threshold
	})

	ctx := context.Background()
	if err := scanner.Scan(ctx, root); err != nil {
		t.Fatalf("scan: %v", err)
	}

	// node_modules should be shallow (via pattern, not size).
	nmPath := filepath.Join(root, "node_modules")
	nmEntry, err := database.GetDir(ctx, nmPath)
	if err != nil {
		t.Fatal(err)
	}
	if nmEntry == nil {
		t.Fatal("node_modules not found")
	}
	if !nmEntry.Shallow {
		t.Error("node_modules should be shallow")
	}
	if nmEntry.Size != 100 {
		t.Errorf("node_modules size: expected 100, got %d", nmEntry.Size)
	}

	// No children (pattern-based shallow skips children).
	children, err := database.GetChildren(ctx, nmPath, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(children) != 0 {
		t.Errorf("node_modules should have no children, got %d", len(children))
	}
}

func TestSizeThresholdPermissionDenied(t *testing.T) {
	// Permission-denied dir during shallowSize should not crash.
	root := t.TempDir()
	database, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	noReadDir := filepath.Join(root, "noread")
	if err := os.MkdirAll(noReadDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(noReadDir, "secret.txt"), []byte("hidden"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "visible.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	os.Chmod(noReadDir, 0o000)
	defer os.Chmod(noReadDir, 0o755)

	scanner := New(database, Config{
		Concurrency:       4,
		BatchSize:         100,
		ChannelSize:       100,
		SmallDirThreshold: 1024,
	})

	ctx := context.Background()
	if err := scanner.Scan(ctx, root); err != nil {
		t.Fatalf("scan should not fail on permission denied: %v", err)
	}

	rootEntry, err := database.GetDir(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if rootEntry == nil {
		t.Fatal("root should be in DB")
	}
}
