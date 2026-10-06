package database

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
)

// lockTailnet serializes writers for one tailnet. Readers do not call it.
// Two tailnets therefore do not share a Go lock. SQLite still runs one write
// transaction at a time, on the writer connection.
func (s *SQLiteStore) lockTailnet(tailnetID string) func() {
	mu := s.tailnetLock(tailnetID)
	mu.Lock()
	return mu.Unlock
}

func (s *SQLiteStore) tailnetLock(tailnetID string) *sync.Mutex {
	v, _ := s.tailnetLocks.LoadOrStore(tailnetID, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// beginWrite starts a transaction on the single writer connection.
// The caller holds that tailnet's write lock. writeStarted, when set, runs
// after BEGIN and before the write statements. The hook must not start
// another write: this call already holds the only writer connection.
func (s *SQLiteStore) beginWrite(ctx context.Context, tailnetID string) (*sql.Tx, error) {
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	if s.writeStarted != nil {
		s.writeStarted(tailnetID)
	}
	return tx, nil
}

// commitWrite runs beforeCommit while the write transaction is still open,
// then commits. A read issued from that hook, or from another goroutine,
// uses the read pool and sees the previous commit until this one lands.
func (s *SQLiteStore) commitWrite(tx *sql.Tx, tailnetID string) error {
	if s.beforeCommit != nil {
		s.beforeCommit(tailnetID)
	}
	return tx.Commit()
}

// beginRead starts a deferred read transaction on the read pool. WAL keeps
// the snapshot stable across every statement in the transaction and does not
// block the writer connection.
func (s *SQLiteStore) beginRead(ctx context.Context) (*sql.Tx, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin read transaction: %w", err)
	}
	return tx, nil
}
