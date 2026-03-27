package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// UpsertDir inserts or updates a directory entry. Setting pending_deletion=0
// clears any deletion mark from a prior incremental scan.
func (d *DB) UpsertDir(ctx context.Context, path, parentPath, name string, size, mtime, scannedAt int64, shallow bool) error {
	shallowInt := 0
	if shallow {
		shallowInt = 1
	}
	_, err := d.db.ExecContext(ctx, `
		INSERT INTO directories (path, parent_path, name, size, mtime, shallow, scanned_at, pending_deletion)
		VALUES (?, ?, ?, ?, ?, ?, ?, 0)
		ON CONFLICT(path) DO UPDATE SET
			parent_path=excluded.parent_path,
			name=excluded.name,
			size=excluded.size,
			mtime=excluded.mtime,
			shallow=excluded.shallow,
			scanned_at=excluded.scanned_at,
			pending_deletion=0
	`, path, parentPath, name, size, mtime, shallowInt, scannedAt)
	if err != nil {
		return fmt.Errorf("upsert dir %s: %w", path, err)
	}
	return nil
}

// GetChildren returns direct children of parentPath, ordered by size descending.
func (d *DB) GetChildren(ctx context.Context, parentPath string, limit, offset int) ([]DirEntry, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT path, parent_path, name, size, mtime, shallow
		FROM directories WHERE parent_path = ?
		ORDER BY size DESC LIMIT ? OFFSET ?
	`, parentPath, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("get children of %s: %w", parentPath, err)
	}
	defer rows.Close()
	return scanDirEntries(rows)
}

// GetTree returns all descendants of parentPath, ordered by size descending.
func (d *DB) GetTree(ctx context.Context, parentPath string) ([]DirEntry, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT path, parent_path, name, size, mtime, shallow
		FROM directories WHERE parent_path = ?
		ORDER BY size DESC
	`, parentPath)
	if err != nil {
		return nil, fmt.Errorf("get tree under %s: %w", parentPath, err)
	}
	defer rows.Close()
	return scanDirEntries(rows)
}

// GetLargestDirs returns the top N non-shallow directories by size.
func (d *DB) GetLargestDirs(ctx context.Context, limit int) ([]DirEntry, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT path, name, size FROM directories
		WHERE shallow = 0 ORDER BY size DESC LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("get largest dirs: %w", err)
	}
	defer rows.Close()

	var entries []DirEntry
	for rows.Next() {
		var e DirEntry
		if err := rows.Scan(&e.Path, &e.Name, &e.Size); err != nil {
			return nil, fmt.Errorf("scan largest dir row: %w", err)
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate largest dir rows: %w", err)
	}
	return entries, nil
}

// GetDir returns a single directory by path. Returns nil if not found.
func (d *DB) GetDir(ctx context.Context, path string) (*DirEntry, error) {
	row := d.db.QueryRowContext(ctx, `
		SELECT path, parent_path, name, size, mtime, shallow
		FROM directories WHERE path = ?
	`, path)

	var e DirEntry
	err := row.Scan(&e.Path, &e.ParentPath, &e.Name, &e.Size, &e.Mtime, &e.Shallow)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get dir %s: %w", path, err)
	}
	return &e, nil
}

// MarkAllForDeletion sets the pending_deletion flag on all directory entries.
func (d *DB) MarkAllForDeletion(ctx context.Context) error {
	_, err := d.db.ExecContext(ctx, `UPDATE directories SET pending_deletion = 1`)
	if err != nil {
		return fmt.Errorf("mark all for deletion: %w", err)
	}
	return nil
}

// DeleteMarked removes all entries flagged for deletion. Returns the number of rows deleted.
func (d *DB) DeleteMarked(ctx context.Context) (int64, error) {
	res, err := d.db.ExecContext(ctx, `DELETE FROM directories WHERE pending_deletion = 1`)
	if err != nil {
		return 0, fmt.Errorf("delete marked dirs: %w", err)
	}
	return res.RowsAffected()
}

// GetDirMtime returns the stored modification time for a directory.
// Returns 0 if the path is not found.
func (d *DB) GetDirMtime(ctx context.Context, path string) (int64, error) {
	row := d.db.QueryRowContext(ctx, `SELECT mtime FROM directories WHERE path = ?`, path)
	var mtime int64
	err := row.Scan(&mtime)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		return 0, fmt.Errorf("get mtime for %s: %w", path, err)
	}
	return mtime, nil
}

// CountDirs returns the total number of directory entries.
func (d *DB) CountDirs(ctx context.Context) (int64, error) {
	row := d.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM directories`)
	var count int64
	err := row.Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count dirs: %w", err)
	}
	return count, nil
}

// scanDirEntries scans rows from a SELECT with columns: path, parent_path, name, size, mtime, shallow.
func scanDirEntries(rows *sql.Rows) ([]DirEntry, error) {
	var entries []DirEntry
	for rows.Next() {
		var e DirEntry
		var shallow int
		if err := rows.Scan(&e.Path, &e.ParentPath, &e.Name, &e.Size, &e.Mtime, &shallow); err != nil {
			return nil, fmt.Errorf("scan dir entry row: %w", err)
		}
		e.Shallow = shallow != 0
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate dir entry rows: %w", err)
	}
	return entries, nil
}
