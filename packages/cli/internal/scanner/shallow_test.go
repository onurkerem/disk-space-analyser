package scanner

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"disk-space-analyser/internal/db"
)

func TestShallowScanning(t *testing.T) {
	root := t.TempDir()
	database, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	// Create tree with node_modules containing nested subdirs and files.
	structure := map[string]int64{
		"file1.txt":                  100,
		"sub1/file2.txt":             200,
		"node_modules/pkg1/file3.txt": 300,
		"node_modules/pkg1/pkg2/file4.txt": 400,
		"node_modules/pkg2/file5.txt": 500,
	}
	createTestTree(t, root, structure)

	scanner := New(database, Config{Concurrency: 4, BatchSize: 100, ChannelSize: 100})
	ctx := context.Background()
	if err := scanner.Scan(ctx, root); err != nil {
		t.Fatalf("scan: %v", err)
	}

	// node_modules should exist as a single shallow entry.
	nmPath := filepath.Join(root, "node_modules")
	nmEntry, err := database.GetDir(ctx, nmPath)
	if err != nil {
		t.Fatal(err)
	}
	if nmEntry == nil {
		t.Fatal("node_modules entry not found in DB")
	}
	if !nmEntry.Shallow {
		t.Error("node_modules should be marked as shallow")
	}
	// Total size of node_modules: 300 + 400 + 500 = 1200
	if nmEntry.Size != 1200 {
		t.Errorf("node_modules size: expected 1200, got %d", nmEntry.Size)
	}

	// node_modules should have NO children in DB.
	children, err := database.GetChildren(ctx, nmPath, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(children) != 0 {
		t.Errorf("node_modules should have no children, got %d", len(children))
	}

	// Root should include node_modules' size in its total.
	// Root direct = 100, sub1 = 200, node_modules = 1200, total = 1500
	rootEntry, err := database.GetDir(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if rootEntry == nil {
		t.Fatal("root not found")
	}
	if rootEntry.Size != 1500 {
		t.Errorf("root size: expected 1500, got %d", rootEntry.Size)
	}
}

func TestShallowPatternAll(t *testing.T) {
	// Verify all default shallow patterns are recognized.
	patterns := []string{"node_modules", ".git", "venv", ".venv", "__pycache__", ".gradle", ".cache"}
	for _, p := range patterns {
		if !DefaultShallowPatterns[p] {
			t.Errorf("pattern %q should be in DefaultShallowPatterns", p)
		}
	}
}

func TestNonShallowDirectory(t *testing.T) {
	root := t.TempDir()
	database, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	// Create deeply nested normal dirs (no shallow patterns).
	structure := map[string]int64{
		"a/b/c/file.txt": 100,
		"a/b/d/file.txt": 200,
	}
	createTestTree(t, root, structure)

	scanner := New(database, Config{Concurrency: 4, BatchSize: 100, ChannelSize: 100})
	ctx := context.Background()
	if err := scanner.Scan(ctx, root); err != nil {
		t.Fatalf("scan: %v", err)
	}

	// All levels should appear: root, a, b, c, d
	count, err := database.CountDirs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if count != 5 {
		t.Errorf("expected 5 entries (root, a, b, c, d), got %d", count)
	}

	// None should be shallow.
	entries, _ := database.GetTree(ctx, root)
	for _, e := range entries {
		if e.Shallow {
			t.Errorf("entry %s should not be shallow", e.Path)
		}
	}
}

func TestShallowSize(t *testing.T) {
	root := t.TempDir()

	// Create files at multiple nesting levels.
	structure := map[string]int64{
		"a/file1.txt":        100,
		"a/b/file2.txt":      200,
		"a/b/c/file3.txt":    300,
	}
	createTestTree(t, root, structure)

	size, err := shallowSize(root)
	if err != nil {
		t.Fatalf("shallowSize: %v", err)
	}
	if size != 600 {
		t.Errorf("expected 600, got %d", size)
	}
}

func TestShallowPermissionDenied(t *testing.T) {
	root := t.TempDir()

	// Create a file, then remove read permission on a subdir.
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

	// Should not error — just skip inaccessible files.
	size, err := shallowSize(root)
	if err != nil {
		t.Fatalf("shallowSize should not fail on permission denied: %v", err)
	}
	// At minimum, the visible file (5 bytes) should be counted.
	if size < 5 {
		t.Errorf("expected at least 5 bytes, got %d", size)
	}
}
