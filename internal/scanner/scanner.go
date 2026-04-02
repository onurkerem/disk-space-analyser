package scanner

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"

	"disk-space-analyser/internal/db"
)

// diskUsage returns actual disk space consumed by a file in bytes.
// Uses stat.Blocks (512-byte blocks) which accounts for sparse files,
// unlike os.FileInfo.Size() which returns the apparent (logical) size.
func diskUsage(info os.FileInfo) int64 {
	if sys, ok := info.Sys().(*syscall.Stat_t); ok {
		return sys.Blocks * 512 // Blocks are in 512-byte units
	}
	return info.Size()
}

// scanEntry represents a single directory to be persisted.
type scanEntry struct {
	Path       string
	ParentPath string
	Name       string
	Size       int64
	Mtime      int64
	Shallow    bool
	SkipSize   bool // if true, don't overwrite the existing size in DB
}

// ProgressCallback is called periodically during scanning to report progress.
// scannedDirs is the number of directories written to the DB so far.
type ProgressCallback func(scannedDirs int64)

// Config holds tuning parameters for the scanner.
type Config struct {
	Concurrency       int   // max concurrent walker goroutines
	BatchSize         int   // rows per DB transaction
	ChannelSize       int   // buffered channel capacity
	SmallDirThreshold int64 // dirs under this size (bytes) are shallow-scanned; 0 = disabled
	OnProgress        ProgressCallback
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{
		Concurrency:       runtime.NumCPU() * 4,
		BatchSize:         1000,
		ChannelSize:       10000,
		SmallDirThreshold: 1 << 30, // 1 GB
	}
}

// Scanner performs concurrent filesystem scanning and persists results to SQLite.
type Scanner struct {
	db                *db.DB
	concurrency       int
	batchSize         int
	channelSize       int
	shallowPatterns   map[string]bool
	smallDirThreshold int64
	onProgress        ProgressCallback
}

// New creates a new Scanner with the given database and configuration.
func New(database *db.DB, cfg Config) *Scanner {
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = DefaultConfig().Concurrency
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = DefaultConfig().BatchSize
	}
	if cfg.ChannelSize <= 0 {
		cfg.ChannelSize = DefaultConfig().ChannelSize
	}
	smallDirThreshold := cfg.SmallDirThreshold
	// Only use default if caller didn't explicitly set a value.
	// Negative and zero both mean "disabled".
	if smallDirThreshold <= 0 {
		smallDirThreshold = 0
	}
	return &Scanner{
		db:                database,
		concurrency:       cfg.Concurrency,
		batchSize:         cfg.BatchSize,
		channelSize:       cfg.ChannelSize,
		shallowPatterns:   DefaultShallowPatterns,
		smallDirThreshold: smallDirThreshold,
		onProgress:        cfg.OnProgress,
	}
}

// dirInfo tracks a directory's scanning state for size computation.
type dirInfo struct {
	path       string
	parentPath string
	name       string
	directSize int64
	mtime      int64
	children   []string
	totalSize  int64 // computed when all children ready; -1 = not ready
	done       bool  // totalSize has been computed and entry sent
}

// dirResult holds the result of reading a single directory.
type dirResult struct {
	path       string
	parentPath string
	name       string
	directSize int64 // sum of immediate file sizes (not including subdirs)
	mtime      int64
	subdirs    []string // discovered subdirectory paths
}

// Scan walks the directory tree rooted at root, computes per-directory sizes,
// and persists all directory entries to the database.
//
// Incremental scanning: if a directory's mtime matches what's stored in DB,
// it is skipped entirely (including all descendants). Directories no longer
// present on disk are removed from DB via mark-and-sweep.
//
// Shallow scanning: directories matching DefaultShallowPatterns have their
// total size computed recursively but no child entries are persisted.
//
// Architecture:
//  1. Worker pool pulls directories from a work channel, reads them with os.ReadDir,
//     and sends dirResult values to a result channel.
//  2. A single coordinator goroutine collects results, dispatches new work, tracks
//     parent-child relationships, and computes subtree sizes bottom-up. When a
//     directory's total size is known, it sends a scanEntry to the entry channel.
//  3. A single writer goroutine batches entries and writes to SQLite.
func (s *Scanner) Scan(ctx context.Context, root string) error {
	info, err := os.Stat(root)
	if err != nil {
		return fmt.Errorf("stat root %s: %w", root, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("root %s is not a directory", root)
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve absolute path %s: %w", root, err)
	}

	// Pre-fetch all known mtimes for incremental skip checking.
	// This avoids DB reads during the coordinator loop, which would deadlock
	// with the writer's write transactions on the single-writer SQLite connection.
	storedMtimes, err := s.db.GetAllMtimes(ctx)
	if err != nil {
		return fmt.Errorf("fetch stored mtimes: %w", err)
	}

	// Mark all existing entries for deletion (incremental: re-scanned dirs clear this flag).
	if err := s.db.MarkAllForDeletion(ctx); err != nil {
		return fmt.Errorf("mark for deletion: %w", err)
	}

	workCh := make(chan string, s.channelSize)
	dirResultCh := make(chan dirResult, s.channelSize)
	entryCh := make(chan scanEntry, s.channelSize)

	var workerWg sync.WaitGroup

	// Start worker pool. Workers read from workCh and send results to dirResultCh.
	for i := 0; i < s.concurrency; i++ {
		workerWg.Add(1)
		go func() {
			defer workerWg.Done()
			for dir := range workCh {
				log.Printf("scanner: worker reading %s", dir)
				select {
				case <-ctx.Done():
					log.Printf("scanner: worker context done, exiting")
					return
				default:
				}
				dirResultCh <- readDir(dir)
				log.Printf("scanner: worker done reading %s", dir)
			}
			log.Printf("scanner: worker exiting (workCh closed)")
		}()
	}

	// Close dirResultCh once all workers have exited.
	go func() {
		workerWg.Wait()
		close(dirResultCh)
	}()

	// Start writer.
	var writerErr error
	var writerWg sync.WaitGroup
	writerWg.Add(1)
	go func() {
		defer writerWg.Done()
		writerErr = s.writer(ctx, entryCh)
	}()

	// Coordinator state (single goroutine, no locks needed).
	dirs := make(map[string]*dirInfo)
	dispatched := make(map[string]bool)
	var pending int64 // directories in flight (dispatched but result not received)

	// Check root mtime — skip if unchanged (incremental optimization).
	rootMtime := info.ModTime().UnixMilli()
	if storedMtime, ok := storedMtimes[absRoot]; ok && storedMtime == rootMtime {
		log.Printf("scanner: root %s unchanged (mtime match), skipping scan", absRoot)
		// Root is unchanged — clear all deletion marks and return.
		// (MarkAllForDeletion was already called above.)
		if err := s.db.ClearDeletionMarks(ctx); err != nil {
			return fmt.Errorf("clear deletion marks: %w", err)
		}
		close(workCh)
		for range dirResultCh {
			// drain — nothing expected
		}
		close(entryCh)
		writerWg.Wait()
		return nil
	}

	// Send root.
	dispatched[absRoot] = true
	atomic.AddInt64(&pending, 1)
	log.Printf("scanner: sending root %s to workCh (concurrency=%d)", absRoot, s.concurrency)
	workCh <- absRoot
	log.Printf("scanner: root sent, entering result loop")

	// Process results from workers.
	for result := range dirResultCh {
		log.Printf("scanner: received result for %s", result.path)
		atomic.AddInt64(&pending, -1)

		di := &dirInfo{
			path:       result.path,
			parentPath: result.parentPath,
			name:       result.name,
			directSize: result.directSize,
			mtime:      result.mtime,
			totalSize:  -1, // not yet computed
			done:       false,
		}
		dirs[result.path] = di

		// Dispatch subdirectories with shallow/mtime filtering.
		var actualChildren []string // children that will be scanned deeply
		var shallowSizes []int64  // sizes of shallow children (added to parent's directSize)
		for _, sub := range result.subdirs {
			basename := filepath.Base(sub)

			// Shallow check: if matches pattern, compute total size and send entry directly.
			if s.shallowPatterns[basename] {
				size, err := shallowSize(sub)
				if err != nil {
					log.Printf("scanner: shallow scan %s error: %v", sub, err)
					size = 0
				}
				subInfo, _ := os.Stat(sub)
				var subMtime int64
				if subInfo != nil {
					subMtime = subInfo.ModTime().UnixMilli()
				}
				select {
				case entryCh <- scanEntry{
					Path:       sub,
					ParentPath: result.path,
					Name:       basename,
					Size:       size,
					Mtime:      subMtime,
					Shallow:    true,
				}:
				case <-ctx.Done():
				}
				log.Printf("scanner: shallow dir %s size=%d", sub, size)
				shallowSizes = append(shallowSizes, size)
				continue // don't add to children — size already includes all contents
			}

			// Size-threshold check: if enabled and subdir total size is under threshold,
			// treat as shallow (compute total size but don't persist child entries).
			if s.smallDirThreshold > 0 {
				size, err := shallowSize(sub)
				if err != nil {
					log.Printf("scanner: size-threshold check %s error: %v", sub, err)
					size = 0
				}
				if size < s.smallDirThreshold {
					subInfo, _ := os.Stat(sub)
					var subMtime int64
					if subInfo != nil {
						subMtime = subInfo.ModTime().UnixMilli()
					}
					select {
					case entryCh <- scanEntry{
						Path:       sub,
						ParentPath: result.path,
						Name:       basename,
						Size:       size,
						Mtime:      subMtime,
						Shallow:    true,
					}:
					case <-ctx.Done():
					}
					log.Printf("scanner: small dir %s size=%d (below threshold %d)", sub, size, s.smallDirThreshold)
					shallowSizes = append(shallowSizes, size)
					continue
				}
			}

			// Mtime check: skip if unchanged (incremental optimization).
			subInfo, subErr := os.Stat(sub)
			if subErr == nil {
				subMtime := subInfo.ModTime().UnixMilli()
				if sm, ok := storedMtimes[sub]; ok && sm == subMtime {
					log.Printf("scanner: skipping unchanged dir %s", sub)
					// Re-upsert to clear pending_deletion flag so it's not deleted.
					select {
					case entryCh <- scanEntry{
						Path:       sub,
						ParentPath: result.path,
						Name:       filepath.Base(sub),
						Size:       0, // size not re-computed; DB retains old value
						Mtime:      subMtime,
						Shallow:    false,
						SkipSize:   true, // signal to writer: don't overwrite size
					}:
					case <-ctx.Done():
					}
					continue // skip this subtree entirely
				}
			}

			if !dispatched[sub] {
				dispatched[sub] = true
				atomic.AddInt64(&pending, 1)
				actualChildren = append(actualChildren, sub)
			}
		}

		// Dispatch deeply-scanned children to workers.
		for _, sub := range actualChildren {
			select {
			case workCh <- sub:
			case <-ctx.Done():
			}
		}

		// Use only deeply-scanned children for size computation.
		di.children = actualChildren

		// Add shallow children sizes to directSize so parent total includes them.
		for _, ss := range shallowSizes {
			di.directSize += ss
		}

		// Check if this directory is ready (no children).
		if len(actualChildren) == 0 {
			// Leaf directory — compute size and propagate.
			di.totalSize = di.directSize
			di.done = true
			s.sendEntry(ctx, di, entryCh)
			s.notifyParent(ctx, di.parentPath, dirs, entryCh)
		} else {
			// Check if any children are already done.
			remaining := 0
			for _, childPath := range actualChildren {
				if child, ok := dirs[childPath]; ok && child.done {
					// Child already computed — count it.
				} else {
					remaining++
				}
			}
			if remaining == 0 {
				// All children already done — compute size now.
				s.computeAndPropagate(ctx, di, dirs, entryCh)
			}
			// Otherwise, we'll be notified when remaining children finish.
		}

		// If no more directories in flight, close workCh so workers exit.
		if atomic.LoadInt64(&pending) == 0 {
			log.Printf("scanner: pending=0, closing workCh")
			close(workCh)
		}
	}

	// dirResultCh is closed (all workers done). All directories processed.
	close(entryCh)
	writerWg.Wait()

	// Delete directories that were marked for deletion but not re-scanned.
	deleted, err := s.db.DeleteMarked(ctx)
	if err != nil {
		return fmt.Errorf("delete marked: %w", err)
	}
	if deleted > 0 {
		log.Printf("scanner: deleted %d stale directory entries", deleted)
	}

	if writerErr != nil {
		return fmt.Errorf("writer: %w", writerErr)
	}
	return nil
}

// sendEntry sends a scanEntry for a directory whose total size is known.
func (s *Scanner) sendEntry(ctx context.Context, di *dirInfo, entryCh chan<- scanEntry) {
	select {
	case entryCh <- scanEntry{
		Path:       di.path,
		ParentPath: di.parentPath,
		Name:       di.name,
		Size:       di.totalSize,
		Mtime:      di.mtime,
		Shallow:    false,
	}:
	case <-ctx.Done():
	}
}

// notifyParent tells the parent that one of its children has been computed.
// If the parent is already in dirs and all children are done, compute the
// parent's size and propagate further.
func (s *Scanner) notifyParent(ctx context.Context, parentPath string, dirs map[string]*dirInfo, entryCh chan<- scanEntry) {
	if parentPath == "" {
		return
	}
	parent, ok := dirs[parentPath]
	if !ok {
		return // parent not yet received from worker
	}
	if parent.done {
		return // already computed
	}

	// Check if all children are done.
	allDone := true
	for _, childPath := range parent.children {
		child, ok := dirs[childPath]
		if !ok || !child.done {
			allDone = false
			break
		}
	}
	if allDone {
		s.computeAndPropagate(ctx, parent, dirs, entryCh)
	}
}

// computeAndPropagate computes a directory's total size from its direct size
// and children's total sizes, sends an entry, and notifies the parent.
func (s *Scanner) computeAndPropagate(ctx context.Context, di *dirInfo, dirs map[string]*dirInfo, entryCh chan<- scanEntry) {
	totalSize := di.directSize
	for _, childPath := range di.children {
		if child, ok := dirs[childPath]; ok {
			totalSize += child.totalSize
		}
	}
	di.totalSize = totalSize
	di.done = true
	s.sendEntry(ctx, di, entryCh)
	s.notifyParent(ctx, di.parentPath, dirs, entryCh)
}

// readDir reads a single directory (non-recursively) and returns its direct
// file size and discovered subdirectories.
func readDir(dir string) dirResult {
	entries, err := os.ReadDir(dir)
	if err != nil {
		log.Printf("scanner: read dir %s: %v", dir, err)
		return dirResult{path: dir, parentPath: filepath.Dir(dir), name: filepath.Base(dir)}
	}

	dirInfo, err := os.Stat(dir)
	if err != nil {
		log.Printf("scanner: stat dir %s: %v", dir, err)
		return dirResult{path: dir, parentPath: filepath.Dir(dir), name: filepath.Base(dir)}
	}

	var directSize int64
	var subdirs []string
	for _, entry := range entries {
		fullPath := filepath.Join(dir, entry.Name())
		if entry.IsDir() {
			subdirs = append(subdirs, fullPath)
		} else {
			info, err := entry.Info()
			if err != nil {
				log.Printf("scanner: stat file %s: %v", fullPath, err)
				continue
			}
			directSize += diskUsage(info)
		}
	}

	return dirResult{
		path:       dir,
		parentPath: filepath.Dir(dir),
		name:       filepath.Base(dir),
		directSize: directSize,
		mtime:      dirInfo.ModTime().UnixMilli(),
		subdirs:    subdirs,
	}
}
