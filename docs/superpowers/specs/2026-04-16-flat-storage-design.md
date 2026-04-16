# Storage: Remove Tiered Aggregation

**Date:** 2026-04-16

## Problem

The database has 13 tables implementing a three-tier aggregation scheme (minutely/hourly/daily). Every poll writes the same data to all three tiers simultaneously. The hourly and daily tiers lose information — most critically, `ports` is overwritten on each upsert (not merged), and time resolution within a bucket is discarded. The `selectTable()` logic silently switches which tier is queried based on time range, which caused the data instability bug in #163. The complexity is not justified: at tsflow's scale, SQLite handles tens of millions of minutely rows fine.

## Design

### New schema (5 tables)

Replace all 13 data tables with:

- `node_pairs` — per-minute `(bucket, src_node_id, dst_node_id, traffic_type)` aggregates with tx/rx bytes, flow count, protocols JSON, ports JSON. Was `node_pairs_minutely`.
- `bandwidth` — per-minute total tx/rx. Was `bandwidth_minutely`.
- `bandwidth_by_node` — per-minute per-node tx/rx. Was `bandwidth_by_node_minutely`.
- `traffic_stats` — per-minute network-wide protocol/traffic-type breakdown. Was `traffic_stats_minutely`.
- `poll_state` — unchanged.

**Dropped:** `*_hourly`, `*_daily`, `flow_logs_current` (was already ephemeral, kept ≤10 min).

### Migration

`Init()` runs `ALTER TABLE … RENAME TO …` to rename the four minutely tables to their new names before applying the new schema. Existing minutely data is preserved. Old hourly/daily tables are dropped with `DROP TABLE IF EXISTS`.

Migration is idempotent: if the new table names already exist (re-running Init), the `ALTER TABLE` is skipped via `IF NOT EXISTS` guards on the new tables.

### Query-time bucketing

`GetBandwidth` and `GetNodeBandwidth` pick resolution from the requested window:

| Window | Resolution | SQL |
|---|---|---|
| ≤ 2 hours | 1 min (raw) | `bucket` as-is |
| ≤ 48 hours | 1 hour | `(bucket / 3600) * 3600` |
| otherwise | 1 day | `(bucket / 86400) * 86400` |

All other query functions (`GetTopTalkers`, `GetTopPairs`, `GetTrafficStats`, `GetNodeStats`, `GetNodePairAggregates`) sum raw minutely rows directly — no bucketing, same query shape as today.

`selectTable()` is deleted. Every query references exactly one table by name.

### Retention

Single `TSFLOW_RETENTION` env var (default `720h` / 30 days) replaces the three `TSFLOW_RETENTION_MINUTELY/HOURLY/DAILY` vars. `Cleanup()` deletes rows from all four data tables where `bucket < now - retention`.

`PollerConfig.Retention time.Duration` replaces `RetentionMinutely`, `RetentionHourly`, `RetentionDaily`.

## Files Changed

| File | Change |
|---|---|
| `backend/internal/database/schema.go` | New 5-table schema; `Init()` migration renames minutely tables, drops old tiers |
| `backend/internal/database/aggregate_queries.go` | Remove `selectTable`, tier upsert loops; add `resolveBucketSize()` for bandwidth queries; simplify all Get* functions |
| `backend/internal/database/maintenance.go` | `Cleanup(ctx, retention)` single arg; `GetDataRange`/`GetStats` reference `node_pairs` only |
| `backend/internal/database/models.go` | `Store` interface: remove `bucketSize` from `UpsertBandwidth`/`UpsertNodeBandwidth`; `Cleanup` single `time.Duration` arg |
| `backend/internal/services/poller.go` | `PollerConfig`: replace three retention fields with `Retention time.Duration`; default `720h` |
| `backend/internal/config/config.go` | Replace three `TSFLOW_RETENTION_*` vars with `TSFLOW_RETENTION`; default `720h` |
| `backend/main.go` | Wire single `Retention` from config to poller |
| `README.md` | Update env vars table |

No frontend, handler, or API shape changes.
