# Analytics: Device Owner Column + Instability Fix

**Issue:** [#163](https://github.com/rajsinghtech/tsflow/issues/163)  
**Date:** 2026-04-16

## Problem Summary

Two issues reported by user:

1. **Analytics instability** — the page initially shows a handful of MB, then jumps to many GB after a few minutes (correct "last 1h" value), then reverts to MB again about a minute later.
2. **Missing owner info** — Top Talkers and Top Pairs tables show device names but not who owns each device.

## Root Cause: Instability

`timeRangeStore` defaults to `'5m'`. On first load, 5 minutes of data is sparse → `liveEmpty` fallback triggers in `loadStats()` → falls back to the full `dataRange` stored in the DB → shows GB. On the 60s auto-refresh, the 5-min window now has some traffic → no longer "empty" → no fallback → shows only 5 min of data (few MB). The cycle repeats.

The `liveEmpty` fallback (stats-store.ts lines 118–139) silently switches what time window is being displayed, which is the core problem.

## Design

### Fix 1: Remove the liveEmpty fallback

**`frontend/src/lib/stores/stats-store.ts`**

Delete the `liveEmpty` fallback block entirely. Stats always reflect the selected time window — no silent substitution.

### Fix 2: Analytics page default to 1h

**`frontend/src/routes/analytics/+page.svelte`**

In `onMount`, if `timeRangeStore.selected === '5m'` (the global default), set it to `'1h'`. This gives analytics a meaningful default without overriding a user's explicit selection.

Add an empty-state message when `statsSummary.totalFlows === 0` and `dataRange` has data:
> "No data in the selected window. Switch to Historical mode to browse stored data."

### Fix 3: Device owner in backend cache

**`backend/internal/services/device_cache.go`**

- Add `Owner string` to `DeviceCacheEntry`
- In `Update()`, populate `Owner` from `d.User`

### Fix 4: resolveNodeOwner helper

**`backend/internal/handlers/handlers.go`**

Add `resolveNodeOwner(nodeIDOrIP string) string` — mirrors `resolveNodeName`, returns `entry.Owner` (empty string if not found).

### Fix 5: Include owner in stats API responses

**`backend/internal/handlers/stats_handlers.go`**

`GetTopTalkers`:
- Add `owner string` to `talkerAccum`
- On first insert into `merged`, set `owner = h.resolveNodeOwner(resolvedID)`
- Include `"owner"` in the `gin.H` response

`GetTopPairs`:
- Add `srcOwner string` and `dstOwner string` to `pairAccum`
- On first insert into `pairMerged`, set owners from `resolveNodeOwner`
- Include `"srcOwner"` and `"dstOwner"` in the `gin.H` response

### Fix 6: Frontend types

**`frontend/src/lib/types/index.ts`**

- Add `owner?: string` to `TopTalker`
- Add `srcOwner?: string` and `dstOwner?: string` to `TopPair`

### Fix 7: Owner display in analytics tables

**`frontend/src/routes/analytics/+page.svelte`**

**Top Talkers table:** Add an **Owner** column after the Device column. Display the full email; show `—` when `owner` is empty (external IPs, unresolved devices). The column is sortable.

**Top Pairs table:** Show owner **stacked below the device name** within the existing Source and Destination cells (smaller muted text), rather than adding two new columns. This keeps the table width manageable. Format: device name on top, `owner` in smaller text below. Show nothing extra when owner is empty.

## Files Changed

| File | Change |
|------|--------|
| `backend/internal/services/device_cache.go` | Add `Owner` to `DeviceCacheEntry`, populate in `Update()` |
| `backend/internal/handlers/handlers.go` | Add `resolveNodeOwner` helper |
| `backend/internal/handlers/stats_handlers.go` | Include `owner`/`srcOwner`/`dstOwner` in API responses |
| `frontend/src/lib/types/index.ts` | Add owner fields to `TopTalker` and `TopPair` |
| `frontend/src/lib/stores/stats-store.ts` | Remove `liveEmpty` fallback |
| `frontend/src/routes/analytics/+page.svelte` | Default to 1h, empty state, owner columns |
