package metrics

import (
	"context"
	"time"

	"github.com/rajsinghtech/tsflow/backend/internal/database"
)

// SQLiteStore adapts the SQLite store to Store.
func SQLiteStore(s *database.SQLiteStore) Store {
	if s == nil {
		return nil
	}
	return sqliteStore{s}
}

type sqliteStore struct{ s *database.SQLiteStore }

func (a sqliteStore) GetPollStateTime(ctx context.Context, tailnetID string) (time.Time, error) {
	state, err := a.s.GetPollState(ctx, tailnetID)
	if err != nil || state == nil {
		return time.Time{}, err
	}
	return state.LastPollEnd, nil
}

func (a sqliteStore) NewestPairBucket(ctx context.Context, tailnetID string) (time.Time, bool, error) {
	return a.s.NewestPairBucket(ctx, tailnetID)
}

func (a sqliteStore) DiskBytes() int64 { return a.s.DiskBytes() }
