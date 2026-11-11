package database

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// Every pooled connection, not just the first, gets the configured cache and
// mmap. A recycled writer used to fall back to SQLite's 2 MiB default.
func TestSQLiteOptionsReachEveryConnection(t *testing.T) {
	store, err := NewSQLiteStoreWithOptions(filepath.Join(t.TempDir(), "test.db"), SQLiteOptions{
		WriterCacheBytes: 256 << 20,
		MmapBytes:        64 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()

	check := func(name string, db *sql.DB, conns int, wantCache int64) {
		t.Helper()
		held := make([]*sql.Conn, 0, conns)
		defer func() {
			for _, c := range held {
				c.Close()
			}
		}()
		for i := 0; i < conns; i++ {
			c, err := db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			held = append(held, c)
			var cache, mmap, busy int64
			if err := c.QueryRowContext(ctx, "PRAGMA cache_size").Scan(&cache); err != nil {
				t.Fatal(err)
			}
			if err := c.QueryRowContext(ctx, "PRAGMA mmap_size").Scan(&mmap); err != nil {
				t.Fatal(err)
			}
			if err := c.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busy); err != nil {
				t.Fatal(err)
			}
			if cache != wantCache || mmap != 64<<20 || busy != 5000 {
				t.Fatalf("%s conn %d: cache_size=%d mmap_size=%d busy_timeout=%d; want %d, %d, 5000",
					name, i, cache, mmap, busy, wantCache, int64(64<<20))
			}
		}
	}
	check("reader", store.db, 3, -readerCacheKiB)
	check("writer", store.writer, 1, -(256 << 10))

	// A fresh writer connection, as after ConnMaxLifetime, keeps the cache.
	store.writer.SetMaxIdleConns(0)
	check("recycled writer", store.writer, 1, -(256 << 10))
}
