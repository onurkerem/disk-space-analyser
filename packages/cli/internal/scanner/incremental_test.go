package scanner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"disk-space-analyser/internal/db"
)

func TestIncrementalFirstScan(t *testing.T) {
	root := t.TempDir()
	database, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	structure := map[string]int64{
		"sub1/file1.txt": 100,
		"sub2/file2.txt": 200,
	}
	createTestTree(t, root, structure)

	scanner := New(database, Config{Concurrency: 4, BatchSize: 100, ChannelSize: 100})
	ctx := context.Background()
	if err := scanner.Scan(ctx, root); err != nil {
		t.Fatalf("first scan: %v", err)
	}

	// First scan should index all dirs: root, sub1, sub2
	count, err := database.CountDirs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Errorf("expected 3 entries, got %d", count)
	}
}

func TestIncrementalSkipUnchanged(t *testing.T) {
	root := t.TempDir()
	database, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	structure := map[string]int64{
		"sub1/file1.txt": 100,
		"sub2/file2.txt": 200,
	}
	createTestTree(t, root, structure)

	scanner := New(database, Config{Concurrency: 4, BatchSize: 100, ChannelSize: 100})
	ctx := context.Background()

	// First scan.
	if err := scanner.Scan(ctx, root); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	count1, _ := database.CountDirs(ctx)

	// Second scan immediately — nothing changed.
	start := time.Now()
	if err := scanner.Scan(ctx, root); err != nil {
		t.Fatalf("second scan: %v", err)
	}
	elapsed := time.Since(start)

	count2, _ := database.CountDirs(ctx)
	if count1 != count2 {
		t.Errorf("count changed: was %d, now %d", count1, count2)
	}

	// Second scan should be very fast (skips everything).
	if elapsed > 2*time.Second {
		t.Errorf("second scan took too long (%v) — incremental skip likely not working", elapsed)
	}
}

func TestIncrementalDetectNewDir(t *testing.T) {
	root := t.TempDir()
	database, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	structure := map[string]int64{
		"sub1/file1.txt": 100,
	}
	createTestTree(t, root, structure)

	scanner := New(database, Config{Concurrency: 4, BatchSize: 100, ChannelSize: 100})
	ctx := context.Background()

	// First scan.
	if err := scanner.Scan(ctx, root); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	count1, _ := database.CountDirs(ctx)

	// Add a new subdirectory with files.
	time.Sleep(100 * time.Millisecond) // ensure mtime difference
	newDir := filepath.Join(root, "newsub")
	if err := os.MkdirAll(newDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(newDir, "newfile.txt"), make([]byte, 50), 0o644); err != nil {
		t.Fatal(err)
	}
	// Touch the root to update its mtime (so it's not skipped).
	os.Chtimes(root, time.Now(), time.Now())

	// Rescan.
	if err := scanner.Scan(ctx, root); err != nil {
		t.Fatalf("rescan: %v", err)
	}

	count2, _ := database.CountDirs(ctx)
	if count2 <= count1 {
		t.Errorf("expected more entries after adding dir (was %d, now %d)", count1, count2)
	}

	// Verify the new dir exists.
	newEntry, err := database.GetDir(ctx, newDir)
	if err != nil {
		t.Fatal(err)
	}
	if newEntry == nil {
		t.Fatal("new directory not found in DB after rescan")
	}
	if newEntry.Size != 50 {
		t.Errorf("new dir size: expected 50, got %d", newEntry.Size)
	}
}

func TestIncrementalDetectModifiedDir(t *testing.T) {
	root := t.TempDir()
	database, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	structure := map[string]int64{
		"sub1/file1.txt": 100,
	}
	createTestTree(t, root, structure)

	scanner := New(database, Config{Concurrency: 4, BatchSize: 100, ChannelSize: 100})
	ctx := context.Background()

	// First scan.
	if err := scanner.Scan(ctx, root); err != nil {
		t.Fatalf("first scan: %v", err)
	}

	sub1Path := filepath.Join(root, "sub1")
	entry1, _ := database.GetDir(ctx, sub1Path)

	// Modify a file in sub1 — this changes the file's content but sub1's mtime
	// only changes if we write to it. WriteFile updates the parent dir mtime.
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(sub1Path, "newfile.txt"), make([]byte, 200), 0o644); err != nil {
		t.Fatal(err)
	}
	// Touch root so it's not skipped.
	os.Chtimes(root, time.Now(), time.Now())

	// Rescan.
	if err := scanner.Scan(ctx, root); err != nil {
		t.Fatalf("rescan: %v", err)
	}

	entry2, err := database.GetDir(ctx, sub1Path)
	if err != nil {
		t.Fatal(err)
	}
	if entry2 == nil {
		t.Fatal("sub1 not found after rescan")
	}
	// sub1 size should have increased: was 100, now 100 + 200 = 300
	if entry2.Size <= entry1.Size {
		t.Errorf("sub1 size should have increased: was %d, now %d", entry1.Size, entry2.Size)
	}
}

func TestIncrementalDetectDeletedDir(t *testing.T) {
	root := t.TempDir()
	database, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	structure := map[string]int64{
		"sub1/file1.txt": 100,
		"sub2/file2.txt": 200,
	}
	createTestTree(t, root, structure)

	scanner := New(database, Config{Concurrency: 4, BatchSize: 100, ChannelSize: 100})
	ctx := context.Background()

	// First scan.
	if err := scanner.Scan(ctx, root); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	count1, _ := database.CountDirs(ctx)

	// Delete sub2.
	time.Sleep(10 * time.Millisecond)
	os.RemoveAll(filepath.Join(root, "sub2"))
	// Touch root so it's not skipped.
	os.Chtimes(root, time.Now(), time.Now())

	// Rescan.
	if err := scanner.Scan(ctx, root); err != nil {
		t.Fatalf("rescan: %v", err)
	}

	count2, _ := database.CountDirs(ctx)
	if count2 >= count1 {
		t.Errorf("expected fewer entries after deleting dir (was %d, now %d)", count1, count2)
	}

	// Verify sub2 is gone.
	sub2Path := filepath.Join(root, "sub2")
	entry, err := database.GetDir(ctx, sub2Path)
	if err != nil {
		t.Fatal(err)
	}
	if entry != nil {
		t.Error("sub2 should have been removed from DB")
	}
}
