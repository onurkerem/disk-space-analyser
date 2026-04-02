package scanner

import (
	"os"
	"path/filepath"
	"sync"
)

// DefaultShallowPatterns maps directory basenames that should be shallow-scanned
// (total size computed but children not individually indexed).
var DefaultShallowPatterns = map[string]bool{
	"node_modules":  true,
	".git":          true,
	"venv":          true,
	".venv":         true,
	"__pycache__":   true,
	".gradle":       true,
	".cache":        true,
	".next":         true,
	"dist":          true,
	"build":         true,
	"target":        true,
	".npm":          true,
	".cargo":        true,
	"vendor":        true,
}

// shallowSize recursively walks all files under dirPath and sums their sizes.
// It does NOT send individual directory entries — just the total.
func shallowSize(dirPath string) (int64, error) {
	var totalSize int64
	var mu sync.Mutex

	err := filepath.WalkDir(dirPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip errors (permission denied, etc.)
		}
		if !d.IsDir() {
			info, err := d.Info()
			if err != nil {
				return nil
			}
			mu.Lock()
			totalSize += diskUsage(info)
			mu.Unlock()
		}
		return nil
	})
	return totalSize, err
}
