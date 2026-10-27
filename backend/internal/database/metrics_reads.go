package database

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"time"
)

// NewestPairBucket is the start of the newest stored minute for a tailnet.
// It is one seek on the node_pairs primary key. ok is false when the
// tailnet has no rows.
func (s *SQLiteStore) NewestPairBucket(ctx context.Context, tailnetID string) (time.Time, bool, error) {
	if err := checkTailnetID(tailnetID); err != nil {
		return time.Time{}, false, err
	}
	var bucket sql.NullInt64
	if err := s.db.QueryRowContext(ctx,
		"SELECT MAX(bucket) FROM node_pairs WHERE tailnet_id = ?", tailnetID,
	).Scan(&bucket); err != nil {
		return time.Time{}, false, fmt.Errorf("failed to read newest bucket: %w", err)
	}
	if !bucket.Valid {
		return time.Time{}, false, nil
	}
	return time.Unix(bucket.Int64, 0).UTC(), true, nil
}

// DiskBytes is the size of the database file plus its WAL and shared-memory
// files. It stats the files and does not touch the database.
func (s *SQLiteStore) DiskBytes() int64 {
	if s.dbPath == "" || s.dbPath == ":memory:" {
		return 0
	}
	var total int64
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if info, err := os.Stat(s.dbPath + suffix); err == nil {
			total += info.Size()
		}
	}
	return total
}
