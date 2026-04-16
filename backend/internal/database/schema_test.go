package database

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewSQLiteStore(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}

	// Verify file was created
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Error("database file was not created")
	}
}

func TestInsertAndGetFlowLogs(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()

	logs := []FlowLog{
		{
			LoggedAt:    time.Now().UTC(),
			NodeID:      "node1",
			TrafficType: "virtual",
			Protocol:    6,
			SrcIP:       "100.1.1.1",
			SrcPort:     12345,
			DstIP:       "100.1.1.2",
			DstPort:     80,
			TxBytes:     1000,
			RxBytes:     500,
			TxPkts:      10,
			RxPkts:      5,
		},
		{
			LoggedAt:    time.Now().UTC(),
			NodeID:      "node2",
			TrafficType: "subnet",
			Protocol:    17,
			SrcIP:       "100.1.1.2",
			SrcPort:     54321,
			DstIP:       "192.168.1.1",
			DstPort:     443,
			TxBytes:     2000,
			RxBytes:     1000,
			TxPkts:      20,
			RxPkts:      10,
		},
	}

	count, err := store.InsertFlowLogs(ctx, logs)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Errorf("expected 2 inserted, got %d", count)
	}

	// Query them back
	result, err := store.GetRecentFlowLogs(ctx, time.Now().Add(-1*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 2 {
		t.Errorf("expected 2 logs, got %d", len(result))
	}
}

func TestPollState(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()

	// Initial state should have zero time
	state, err := store.GetPollState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !state.LastPollEnd.IsZero() {
		t.Errorf("expected zero last poll end, got %v", state.LastPollEnd)
	}

	// Update and verify
	now := time.Now().UTC().Truncate(time.Second)
	if err := store.UpdatePollState(ctx, now); err != nil {
		t.Fatal(err)
	}

	state, err = store.GetPollState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.LastPollEnd.Truncate(time.Second) != now {
		t.Errorf("expected %v, got %v", now, state.LastPollEnd)
	}
}

func TestGetDataRange_Empty(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()

	dr, err := store.GetDataRange(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if dr.Count != 0 {
		t.Errorf("expected 0 count, got %d", dr.Count)
	}
}

func TestCleanup_EmptyDB(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()

	deleted, err := store.Cleanup(ctx, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 0 {
		t.Errorf("expected 0 deleted on empty db, got %d", deleted)
	}
}

func setupTestDB(t *testing.T) *SQLiteStore {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	if err := store.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	return store
}
