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

	"disk-space-analyser/internal/db"
)

// scanEntry represents a single directory to be persisted.
type scanEntry struct {
	Path       string
	ParentPath string
	Name       string
	Size       int64
	Mtime      int64
	Shallow    bool
}

// Config holds tuning parameters for the scanner.
type Config struct {
	Concurrency int // max concurrent walker goroutines
	BatchSize   int // rows per DB transaction
	ChannelSize int // buffered channel capacity
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{
		Concurrency: runtime.NumCPU() * 4,
		BatchSize:   1000,
		ChannelSize: 10000,
	}
}

// Scanner performs concurrent filesystem scanning and persists results to SQLite.
type Scanner struct {
	db          *db.DB
	concurrency int
	batchSize   int
	channelSize int
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
	return &Scanner{
		db:          database,
		concurrency: cfg.Concurrency,
		batchSize:   cfg.BatchSize,
		channelSize: cfg.ChannelSize,
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

	workCh := make(chan string, s.channelSize)
	dirResultCh := make(chan dirResult, s.channelSize)
	entryCh := make(chan scanEntry, s.channelSize)

	var workerWg sync.WaitGroup

	// Start worker pool.
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

	// Send root.
	dispatched[absRoot] = true
	atomic.AddInt64(&pending, 1)
	log.Printf("scanner: sending root %s to workCh (concurrency=%d)", absRoot, s.concurrency)
	workCh <- absRoot
	log.Printf("scanner: root sent, entering result loop")

	// Process results from workers.
	for result := range dirResultCh {
		log.Printf("scanner: received result for %s", result.path)
		newPending := atomic.AddInt64(&pending, -1)

		di := &dirInfo{
			path:       result.path,
			parentPath: result.parentPath,
			name:       result.name,
			directSize: result.directSize,
			mtime:      result.mtime,
			children:   result.subdirs,
			totalSize:  -1, // not yet computed
			done:       false,
		}
		dirs[result.path] = di

		// Dispatch subdirectories.
		for _, sub := range result.subdirs {
			if !dispatched[sub] {
				dispatched[sub] = true
				atomic.AddInt64(&pending, 1)
				select {
				case workCh <- sub:
				case <-ctx.Done():
				}
			}
		}

		// Check if this directory is ready (no children).
		if len(result.subdirs) == 0 {
			// Leaf directory — compute size and propagate.
			di.totalSize = di.directSize
			di.done = true
			s.sendEntry(ctx, di, entryCh)
			s.notifyParent(ctx, di.parentPath, dirs, entryCh)
		} else {
			// Check if any children are already done.
			remaining := 0
			for _, childPath := range result.subdirs {
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

		// If no more directories in flight, close workCh.
		if newPending == 0 {
			log.Printf("scanner: pending=0, closing workCh")
			close(workCh)
		}
	}

	// Workers have finished. All directories processed.
	close(entryCh)
	writerWg.Wait()

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
			directSize += info.Size()
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
