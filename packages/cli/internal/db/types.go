package db

// DirEntry represents a scanned directory stored in the database.
type DirEntry struct {
	Path       string
	ParentPath string
	Name       string
	Size       int64
	Mtime      int64
	Shallow    bool
}
