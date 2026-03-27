package scanner

import (
	"context"
	"log"
	"time"

	"disk-space-analyser/internal/db"
)

// writer consumes scanEntry values from results and persists them to the
// database in batches. It flushes when batchSize is reached or after 1 second
// of inactivity, whichever comes first.
func (s *Scanner) writer(ctx context.Context, results <-chan scanEntry) error {
	const flushTimeout = 1 * time.Second
	batch := make([]db.BatchEntry, 0, s.batchSize)
	timer := time.NewTimer(flushTimeout)
	if !timer.Stop() {
		<-timer.C
	}

	database := s.db
	scannedAt := time.Now().UnixMilli()

	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if err := database.BatchUpsert(ctx, batch, scannedAt); err != nil {
			return err
		}
		batch = batch[:0]
		return nil
	}

	defer func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	}()

	for {
		select {
		case entry, ok := <-results:
			if !ok {
				// Channel closed — flush remaining and return.
				if err := flush(); err != nil {
					return err
				}
				return nil
			}
			batch = append(batch, db.BatchEntry{
				Path:       entry.Path,
				ParentPath: entry.ParentPath,
				Name:       entry.Name,
				Size:       entry.Size,
				Mtime:      entry.Mtime,
				Shallow:    entry.Shallow,
			})
			if len(batch) >= s.batchSize {
				if err := flush(); err != nil {
					return err
				}
			}
			// Reset timer on each entry.
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(flushTimeout)

		case <-timer.C:
			if err := flush(); err != nil {
				return err
			}

		case <-ctx.Done():
			if err := flush(); err != nil {
				return err
			}
			log.Printf("scanner: writer cancelled")
			return ctx.Err()
		}
	}
}
