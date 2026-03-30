package db

import (
	"context"
	"testing"
)

func helperNewDB(t *testing.T) *DB {
	t.Helper()
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func helperUpsert(t *testing.T, db *DB, path, parent, name string, size, mtime int64, shallow bool) {
	t.Helper()
	ctx := context.Background()
	if err := db.UpsertDir(ctx, path, parent, name, size, mtime, mtime, shallow); err != nil {
		t.Fatalf("upsert %s: %v", path, err)
	}
}

func TestUpsertAndRetrieve(t *testing.T) {
	db := helperNewDB(t)
	ctx := context.Background()

	helperUpsert(t, db, "/home/user/docs", "/home/user", "docs", 4096, 1710000000, false)

	entry, err := db.GetDir(ctx, "/home/user/docs")
	if err != nil {
		t.Fatalf("get dir: %v", err)
	}
	if entry == nil {
		t.Fatal("expected entry, got nil")
	}
	if entry.Path != "/home/user/docs" {
		t.Errorf("path = %q, want %q", entry.Path, "/home/user/docs")
	}
	if entry.ParentPath != "/home/user" {
		t.Errorf("parent = %q, want %q", entry.ParentPath, "/home/user")
	}
	if entry.Name != "docs" {
		t.Errorf("name = %q, want %q", entry.Name, "docs")
	}
	if entry.Size != 4096 {
		t.Errorf("size = %d, want %d", entry.Size, 4096)
	}
	if entry.Mtime != 1710000000 {
		t.Errorf("mtime = %d, want %d", entry.Mtime, 1710000000)
	}
	if entry.Shallow != false {
		t.Errorf("shallow = %v, want false", entry.Shallow)
	}
}

func TestUpsertOverwrite(t *testing.T) {
	db := helperNewDB(t)
	ctx := context.Background()

	helperUpsert(t, db, "/home/user/docs", "/home/user", "docs", 4096, 1710000000, false)
	helperUpsert(t, db, "/home/user/docs", "/home/user", "docs", 8192, 1710000100, true)

	entry, err := db.GetDir(ctx, "/home/user/docs")
	if err != nil {
		t.Fatalf("get dir: %v", err)
	}
	if entry.Size != 8192 {
		t.Errorf("size = %d, want %d", entry.Size, 8192)
	}
	if entry.Mtime != 1710000100 {
		t.Errorf("mtime = %d, want %d", entry.Mtime, 1710000100)
	}
	if entry.Shallow != true {
		t.Errorf("shallow = %v, want true", entry.Shallow)
	}
}

func TestGetChildren(t *testing.T) {
	db := helperNewDB(t)
	ctx := context.Background()

	helperUpsert(t, db, "/home/user", "/home", "user", 10000, 1, false)
	helperUpsert(t, db, "/home/user/docs", "/home/user", "docs", 5000, 1, false)
	helperUpsert(t, db, "/home/user/pics", "/home/user", "pics", 3000, 1, false)
	helperUpsert(t, db, "/home/user/music", "/home/user", "music", 8000, 1, false)

	children, err := db.GetChildren(ctx, "/home/user", 10, 0)
	if err != nil {
		t.Fatalf("get children: %v", err)
	}
	if len(children) != 3 {
		t.Fatalf("len(children) = %d, want 3", len(children))
	}
	// Ordered by size DESC: music(8000), docs(5000), pics(3000)
	if children[0].Name != "music" {
		t.Errorf("first child = %q, want music", children[0].Name)
	}
	if children[1].Name != "docs" {
		t.Errorf("second child = %q, want docs", children[1].Name)
	}
	if children[2].Name != "pics" {
		t.Errorf("third child = %q, want pics", children[2].Name)
	}
}

func TestGetLargestDirs(t *testing.T) {
	db := helperNewDB(t)
	ctx := context.Background()

	// Insert 5 dirs, 2 are shallow
	helperUpsert(t, db, "/a", "/", "a", 100, 1, false)
	helperUpsert(t, db, "/b", "/", "b", 500, 1, false)
	helperUpsert(t, db, "/c", "/", "c", 300, 1, false)
	helperUpsert(t, db, "/d", "/", "d", 200, 1, true) // shallow — excluded
	helperUpsert(t, db, "/e", "/", "e", 400, 1, false)

	largest, err := db.GetLargestDirs(ctx, 3)
	if err != nil {
		t.Fatalf("get largest: %v", err)
	}
	if len(largest) != 3 {
		t.Fatalf("len = %d, want 3", len(largest))
	}
	if largest[0].Name != "b" || largest[0].Size != 500 {
		t.Errorf("largest[0] = %s/%d, want b/500", largest[0].Name, largest[0].Size)
	}
	if largest[1].Name != "e" || largest[1].Size != 400 {
		t.Errorf("largest[1] = %s/%d, want e/400", largest[1].Name, largest[1].Size)
	}
	if largest[2].Name != "c" || largest[2].Size != 300 {
		t.Errorf("largest[2] = %s/%d, want c/300", largest[2].Name, largest[2].Size)
	}
}

func TestMarkAndDelete(t *testing.T) {
	db := helperNewDB(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		helperUpsert(t, db, "/dir"+string(rune('a'+i)), "/", string(rune('a'+i)), int64(i)*100, 1, false)
	}

	if err := db.MarkAllForDeletion(ctx); err != nil {
		t.Fatalf("mark all: %v", err)
	}
	deleted, err := db.DeleteMarked(ctx)
	if err != nil {
		t.Fatalf("delete marked: %v", err)
	}
	if deleted != 5 {
		t.Errorf("deleted = %d, want 5", deleted)
	}
	count, err := db.CountDirs(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("count = %d, want 0", count)
	}
}

func TestMarkDeletePartial(t *testing.T) {
	db := helperNewDB(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		helperUpsert(t, db, "/dir"+string(rune('a'+i)), "/", string(rune('a'+i)), int64(i)*100, 1, false)
	}

	// Mark all for deletion
	if err := db.MarkAllForDeletion(ctx); err != nil {
		t.Fatalf("mark all: %v", err)
	}

	// Re-upsert 2 of them (clears pending_deletion flag)
	helperUpsert(t, db, "/dirb", "/", "b", 200, 2, false)
	helperUpsert(t, db, "/dird", "/", "d", 400, 2, false)

	deleted, err := db.DeleteMarked(ctx)
	if err != nil {
		t.Fatalf("delete marked: %v", err)
	}
	if deleted != 3 {
		t.Errorf("deleted = %d, want 3", deleted)
	}
	count, err := db.CountDirs(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
}

func TestGetDirMtime(t *testing.T) {
	db := helperNewDB(t)
	ctx := context.Background()

	helperUpsert(t, db, "/home/user", "/home", "user", 1000, 1710000000, false)

	mtime, err := db.GetDirMtime(ctx, "/home/user")
	if err != nil {
		t.Fatalf("get mtime: %v", err)
	}
	if mtime != 1710000000 {
		t.Errorf("mtime = %d, want %d", mtime, 1710000000)
	}

	// Nonexistent path returns 0
	mtime, err = db.GetDirMtime(ctx, "/nonexistent")
	if err != nil {
		t.Fatalf("get mtime nonexistent: %v", err)
	}
	if mtime != 0 {
		t.Errorf("mtime = %d, want 0", mtime)
	}
}

func TestCountDirs(t *testing.T) {
	db := helperNewDB(t)
	ctx := context.Background()

	count, err := db.CountDirs(ctx)
	if err != nil {
		t.Fatalf("count empty: %v", err)
	}
	if count != 0 {
		t.Errorf("count = %d, want 0", count)
	}

	for i := 0; i < 7; i++ {
		helperUpsert(t, db, "/dir"+string(rune('0'+i)), "/", string(rune('0'+i)), int64(i)*100, 1, false)
	}

	count, err = db.CountDirs(ctx)
	if err != nil {
		t.Fatalf("count after insert: %v", err)
	}
	if count != 7 {
		t.Errorf("count = %d, want 7", count)
	}
}

func TestTruncate(t *testing.T) {
	db := helperNewDB(t)
	ctx := context.Background()

	// Insert some rows.
	for i := 0; i < 5; i++ {
		helperUpsert(t, db, "/dir"+string(rune('a'+i)), "/", string(rune('a'+i)), int64(i)*100, 1, false)
	}

	count, err := db.CountDirs(ctx)
	if err != nil {
		t.Fatalf("count before truncate: %v", err)
	}
	if count != 5 {
		t.Fatalf("count before = %d, want 5", count)
	}

	if err := db.Truncate(ctx); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	count, err = db.CountDirs(ctx)
	if err != nil {
		t.Fatalf("count after truncate: %v", err)
	}
	if count != 0 {
		t.Errorf("count after = %d, want 0", count)
	}
}

func TestGetDirNotFound(t *testing.T) {
	db := helperNewDB(t)
	ctx := context.Background()

	entry, err := db.GetDir(ctx, "/nonexistent")
	if err != nil {
		t.Fatalf("get nonexistent: %v", err)
	}
	if entry != nil {
		t.Errorf("expected nil, got %+v", entry)
	}
}
