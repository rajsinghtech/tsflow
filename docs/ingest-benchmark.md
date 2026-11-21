# Ingest benchmark

`TestIngestProdScale` (in `backend/internal/database/ingest_bench_test.go`)
loads synthetic data at the size of a large tailnet and times each phase of
the write path. It uses made-up node ids, ports and byte counts only. It is
skipped unless `TSFLOW_INGEST_BENCH=1`.

Per five-minute object it commits the object the way the object-store poller
does (`CommitObjectIngest`), moves the cursor every third object
(`UpdatePollState`, which rolls complete hours), and at the end runs retention
(`Cleanup`) over the whole run.

```
TSFLOW_INGEST_BENCH=1 go test ./internal/database -run TestIngestProdScale \
  -count=1 -v -timeout 60m -cpuprofile cpu.out -memprofile mem.out
go tool pprof -top -cum cpu.out
```

Settings (all optional): `TSFLOW_INGEST_BENCH_NODES` (5000),
`TSFLOW_INGEST_BENCH_PAIRS` pair rows per minute (40000),
`TSFLOW_INGEST_BENCH_FILES` five-minute objects (24),
`TSFLOW_INGEST_BENCH_OVERLAP` share of rows that hit an existing key (0.15),
`TSFLOW_INGEST_BENCH_DIR` where the database goes (use the same kind of disk
as production), `TSFLOW_INGEST_BENCH_OUT` a file for the JSON result.

The defaults give 200,000 pair rows per object, about 1.6 million flows after
aggregation. A run needs about 10 GB of disk and 2 GB of memory.

## How the write path works

- `node_pairs` rows are normalised, sorted by primary key and merged in
  memory. Keys that already exist are read, merged in Go and updated. New keys
  go in with multi-row INSERTs in key order. The stored JSON text is
  byte-for-byte what the old SQL merge produced
  (`TestNodePairWriteMatchesLegacySQL` compares against the old statement).
- Hourly rollups only run for complete hours, each hour in its own
  transaction after the data commit. They used to re-read and rewrite the
  current hour on every poll.
- Retention deletes minute data in five-minute slices, one short transaction
  each.

## Follow-ups

- The JSON columns are most of each `node_pairs` row. A compact encoding or
  side tables for ports and protocols would shrink the database and every
  read, but readers use SQLite JSON functions in many queries, so it is a
  separate change.
- The three secondary indexes on `node_pairs` cost a random insert each.
  Check which queries still need them.
- Snapshot the database to object storage so a fresh pod restores instead of
  backfilling.
