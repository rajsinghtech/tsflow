package database

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"sync/atomic"
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
	seq := s.commitSeqFor(tailnetID)
	seq.started.Add(1)
	defer seq.finished.Add(1)
	if s.beforeCommit != nil {
		s.beforeCommit(tailnetID)
	}
	return tx.Commit()
}

// commitSeq counts one tailnet's commits. started moves before a commit can
// become visible and finished moves after it is. A reader that finds no
// commit in flight when it starts, and started unchanged after its last
// snapshot, knows every snapshot it took holds the same rows for the
// tailnet: every write commits through commitWrite with its own tailnet ID.
type commitSeq struct {
	started, finished atomic.Uint64
}

func (s *SQLiteStore) commitSeqFor(tailnetID string) *commitSeq {
	if v, ok := s.commitSeqs.Load(tailnetID); ok {
		return v.(*commitSeq)
	}
	v, _ := s.commitSeqs.LoadOrStore(tailnetID, &commitSeq{})
	return v.(*commitSeq)
}

// quiet returns the started count and whether no commit is in flight. Read
// finished first: a commit that starts between the two loads then shows as
// in flight.
func (c *commitSeq) quiet() (uint64, bool) {
	finished := c.finished.Load()
	started := c.started.Load()
	return started, started == finished
}

func (c *commitSeq) unchangedSince(started uint64) bool {
	return c.started.Load() == started
}

// commitWriteTouching commits, then drops the hours the transaction changed
// from the closed-hour cache. The drop must follow the commit: a reader that
// started before it cannot store the old rows.
func (s *SQLiteStore) commitWriteTouching(tx *sql.Tx, tailnetID string, touches hourTouches) error {
	if err := s.commitWrite(tx, tailnetID); err != nil {
		return err
	}
	s.hourCache.invalidate(tailnetID, touches)
	return nil
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
