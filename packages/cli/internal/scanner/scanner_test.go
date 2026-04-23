package scanner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"disk-space-analyser/internal/db"
)

// createTestTree creates directories and files under root according to the
// structure map. Keys are file paths relative to root; values are file sizes.
// Intermediate directories are created automatically.
func createTestTree(t *testing.T, root string, structure map[string]int64) {
	t.Helper()
	for relPath, size := range structure {
		fullPath := filepath.Join(root, relPath)
		dir := filepath.Dir(fullPath)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create dir %s: %v", dir, err)
		}
		if err := os.WriteFile(fullPath, make([]byte, size), 0o644); err != nil {
			t.Fatalf("create file %s: %v", fullPath, err)
		}
	}
}

// computeDiskUsage walks dir and returns the total disk space used by all files.
// Uses block-based measurement (matching the scanner's diskUsage function)
// so test expectations account for actual filesystem block sizes.
func computeDiskUsage(t *testing.T, dir string) int64 {
	t.Helper()
	var total int64
	filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			total += diskUsage(info)
		}
		return nil
	})
	return total
}

func TestScanFlatDirectory(t *testing.T) {
	root := t.TempDir()
	database, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	// Create 5 files in root with known sizes.
	structure := map[string]int64{
		"a.txt": 100,
		"b.txt": 200,
		"c.txt": 300,
		"d.txt": 400,
		"e.txt": 500,
	}
	createTestTree(t, root, structure)

	scanner := New(database, Config{
		Concurrency: 4,
		BatchSize:   100,
		ChannelSize: 100,
	})

	ctx := context.Background()
	if err := scanner.Scan(ctx, root); err != nil {
		t.Fatalf("scan: %v", err)
	}

	// Verify DB has 1 entry (the root dir).
	count, err := database.CountDirs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("expected 1 dir entry, got %d", count)
	}

	// Verify total size matches actual disk usage.
	entry, err := database.GetDir(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if entry == nil {
		t.Fatal("root entry not found in DB")
	}
	expectedSize := computeDiskUsage(t, root)
	if entry.Size != expectedSize {
		t.Errorf("expected size %d, got %d", expectedSize, entry.Size)
	}
}

func TestScanNestedDirectories(t *testing.T) {
	root := t.TempDir()
	database, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	// Create: root/sub1/file1 (100), root/sub2/file2 (200), root/sub3/nested/file3 (300)
	structure := map[string]int64{
		"sub1/file1":    100,
		"sub2/file2":    200,
		"sub3/nested/file3": 300,
	}
	createTestTree(t, root, structure)

	scanner := New(database, Config{Concurrency: 4, BatchSize: 100, ChannelSize: 100})
	ctx := context.Background()
	if err := scanner.Scan(ctx, root); err != nil {
		t.Fatalf("scan: %v", err)
	}

	// Expect 5 entries: root, sub1, sub2, sub3, nested.
	count, err := database.CountDirs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if count != 5 {
		t.Errorf("expected 5 dir entries, got %d", count)
	}

	// Verify root size = 100+200+300 = 600.
	rootEntry, err := database.GetDir(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if rootEntry == nil {
		t.Fatal("root not found")
	}
	if rootEntry.Size != computeDiskUsage(t, root) {
		t.Errorf("root size: expected %d, got %d", computeDiskUsage(t, root), rootEntry.Size)
	}

	// Verify sub3 size = 300 (has nested/file3).
	sub3 := filepath.Join(root, "sub3")
	sub3Entry, err := database.GetDir(ctx, sub3)
	if err != nil {
		t.Fatal(err)
	}
	if sub3Entry == nil {
		t.Fatal("sub3 not found")
	}
	if sub3Entry.Size != computeDiskUsage(t, sub3) {
		t.Errorf("sub3 size: expected %d, got %d", computeDiskUsage(t, sub3), sub3Entry.Size)
	}
}

func TestScanEmptyDirectory(t *testing.T) {
	root := t.TempDir()
	database, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	scanner := New(database, Config{Concurrency: 4, BatchSize: 100, ChannelSize: 100})
	ctx := context.Background()
	if err := scanner.Scan(ctx, root); err != nil {
		t.Fatalf("scan: %v", err)
	}

	count, err := database.CountDirs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("expected 1 dir entry, got %d", count)
	}

	entry, err := database.GetDir(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if entry == nil {
		t.Fatal("root not found")
	}
	if entry.Size != 0 {
		t.Errorf("expected size 0, got %d", entry.Size)
	}
}

func TestScanPermissionDenied(t *testing.T) {
	root := t.TempDir()
	database, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	// Create a subdirectory with files, then remove read permissions.
	noReadDir := filepath.Join(root, "noread")
	if err := os.MkdirAll(noReadDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(noReadDir, "secret.txt"), []byte("hidden"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Remove all permissions.
	os.Chmod(noReadDir, 0o000)
	defer os.Chmod(noReadDir, 0o755) // restore for cleanup

	scanner := New(database, Config{Concurrency: 4, BatchSize: 100, ChannelSize: 100})
	ctx := context.Background()

	// Should not crash — just log and skip.
	if err := scanner.Scan(ctx, root); err != nil {
		t.Fatalf("scan should not fail on permission denied: %v", err)
	}

	// Root should still be in DB.
	entry, err := database.GetDir(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if entry == nil {
		t.Fatal("root should still be in DB after permission error")
	}
}

func TestScanDeepNesting(t *testing.T) {
	root := t.TempDir()
	database, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	// Create 10 levels deep with a file at each level.
	structure := make(map[string]int64)
	current := ""
	for i := 0; i < 10; i++ {
		dirName := filepath.Join(current, "level"+string(rune('0'+i)))
		current = dirName
		structure[filepath.Join(dirName, "file.txt")] = int64((i+1) * 100)
	}
	createTestTree(t, root, structure)

	scanner := New(database, Config{Concurrency: 4, BatchSize: 100, ChannelSize: 100})
	ctx := context.Background()
	if err := scanner.Scan(ctx, root); err != nil {
		t.Fatalf("scan: %v", err)
	}

	// Expect 11 entries: root + 10 levels.
	count, err := database.CountDirs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if count != 11 {
		t.Errorf("expected 11 dir entries, got %d", count)
	}

	// Verify root size matches actual disk usage.
	rootEntry, err := database.GetDir(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if rootEntry == nil {
		t.Fatal("root not found")
	}
	expectedRoot := computeDiskUsage(t, root)
	if rootEntry.Size != expectedRoot {
		t.Errorf("root size: expected %d, got %d", expectedRoot, rootEntry.Size)
	}
}

func TestScanManyFiles(t *testing.T) {
	root := t.TempDir()
	database, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	// Create 500 small files in root.
	structure := make(map[string]int64)
	for i := 0; i < 500; i++ {
		structure[fmt.Sprintf("file_%03d.txt", i)] = 64
	}
	createTestTree(t, root, structure)

	scanner := New(database, Config{Concurrency: 8, BatchSize: 100, ChannelSize: 1000})
	ctx := context.Background()
	if err := scanner.Scan(ctx, root); err != nil {
		t.Fatalf("scan: %v", err)
	}

	// Verify DB has 1 entry.
	count, err := database.CountDirs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("expected 1 dir entry, got %d", count)
	}

	// Verify size = 500 * 64 = 32000.
	entry, err := database.GetDir(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if entry == nil {
		t.Fatal("root not found")
	}
	expectedSize := computeDiskUsage(t, root)
	if entry.Size != expectedSize {
		t.Errorf("expected size %d, got %d", expectedSize, entry.Size)
	}
}

func TestScanNonExistentRoot(t *testing.T) {
	database, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	scanner := New(database, Config{})
	err = scanner.Scan(context.Background(), "/nonexistent/path/that/does/not/exist")
	if err == nil {
		t.Fatal("expected error for non-existent root")
	}
}

func TestScanRootIsFile(t *testing.T) {
	root := t.TempDir()
	filePath := filepath.Join(root, "notadir.txt")
	if err := os.WriteFile(filePath, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	database, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	scanner := New(database, Config{})
	err = scanner.Scan(context.Background(), filePath)
	if err == nil {
		t.Fatal("expected error when root is a file")
	}
}
