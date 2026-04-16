# Analytics Owner Column + Stability Fix Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add device owner (email) to Top Talkers and Top Pairs tables, and fix the instability where analytics data oscillates between a few MB and many GB on auto-refresh.

**Architecture:** Backend changes propagate owner from the Tailscale device cache through the stats API responses. Frontend removes a flawed "liveEmpty" fallback that caused silent time-window switching, defaults the analytics page to 1h, and renders owner inline in the tables.

**Tech Stack:** Go (gin, device cache), TypeScript/Svelte 5 (`$state`, `$derived`), Tailwind CSS

---

## File Map

| File | Change |
|------|--------|
| `backend/internal/services/device_cache.go` | Add `Owner` field to `DeviceCacheEntry`, populate in `Update()` |
| `backend/internal/services/device_cache_test.go` | Add test for owner being stored and retrieved |
| `backend/internal/handlers/handlers.go` | Add `resolveNodeOwner` helper |
| `backend/internal/handlers/handlers_test.go` | Add test for `resolveNodeOwner` |
| `backend/internal/handlers/stats_handlers.go` | Include `owner`/`srcOwner`/`dstOwner` in `GetTopTalkers` and `GetTopPairs` responses |
| `frontend/src/lib/types/index.ts` | Add `owner?`, `srcOwner?`, `dstOwner?` to talker/pair types |
| `frontend/src/lib/stores/stats-store.ts` | Remove `liveEmpty` fallback (lines 118–139) |
| `frontend/src/routes/analytics/+page.svelte` | Default to 1h on mount, add empty-state banner, owner columns in tables |

---

## Task 1: Add Owner to DeviceCacheEntry

**Files:**
- Modify: `backend/internal/services/device_cache.go`
- Modify: `backend/internal/services/device_cache_test.go`

- [ ] **Step 1: Add `Owner` field to `DeviceCacheEntry` and populate it in `Update()`**

In `device_cache.go`, the `DeviceCacheEntry` struct and `Update` method currently look like:

```go
type DeviceCacheEntry struct {
	ID          string
	Name        string
	Hostname    string
	IPs         []string
	IsTailscale bool
}
```

```go
entry := &DeviceCacheEntry{
    ID:          d.ID,
    Name:        d.Name,
    Hostname:    d.Hostname,
    IPs:         d.Addresses,
    IsTailscale: true,
}
```

Change them to:

```go
type DeviceCacheEntry struct {
	ID          string
	Name        string
	Hostname    string
	Owner       string
	IPs         []string
	IsTailscale bool
}
```

```go
entry := &DeviceCacheEntry{
    ID:          d.ID,
    Name:        d.Name,
    Hostname:    d.Hostname,
    Owner:       d.User,
    IPs:         d.Addresses,
    IsTailscale: true,
}
```

- [ ] **Step 2: Write the failing test**

Add to `backend/internal/services/device_cache_test.go`:

```go
func TestDeviceCache_Owner(t *testing.T) {
	cache := NewDeviceCache()
	cache.Update([]Device{
		{
			ID:        "device1",
			Name:      "laptop.example.ts.net",
			Hostname:  "laptop",
			User:      "user@example.com",
			Addresses: []string{"100.1.1.1"},
		},
		{
			ID:        "device2",
			Name:      "server.example.ts.net",
			Hostname:  "server",
			User:      "",
			Addresses: []string{"100.1.1.2"},
		},
	})

	entry := cache.GetDevice("device1")
	if entry == nil {
		t.Fatal("expected device1 entry, got nil")
	}
	if entry.Owner != "user@example.com" {
		t.Errorf("expected owner=user@example.com, got %q", entry.Owner)
	}

	entry2 := cache.GetDevice("device2")
	if entry2 == nil {
		t.Fatal("expected device2 entry, got nil")
	}
	if entry2.Owner != "" {
		t.Errorf("expected empty owner, got %q", entry2.Owner)
	}
}
```

- [ ] **Step 3: Run the test — it will fail until the struct field is added**

```bash
cd backend && go test ./internal/services/ -run TestDeviceCache_Owner -v
```

Expected: FAIL (field doesn't exist yet — unless you did step 1 first, in which case it passes)

- [ ] **Step 4: Confirm test passes after step 1**

```bash
cd backend && go test ./internal/services/ -v
```

Expected: all tests PASS

- [ ] **Step 5: Commit**

```bash
git add backend/internal/services/device_cache.go backend/internal/services/device_cache_test.go
git commit -m "feat: add owner field to DeviceCacheEntry"
```

---

## Task 2: Add resolveNodeOwner Helper

**Files:**
- Modify: `backend/internal/handlers/handlers.go`
- Modify: `backend/internal/handlers/handlers_test.go`

- [ ] **Step 1: Write the failing test**

In `handlers_test.go`, the existing tests use a nil `Handlers{}` because those helpers don't use `poller`. `resolveNodeOwner` does use `poller`, so we need a test that exercises the nil-poller path and a happy path. Add:

```go
func TestResolveNodeOwner_NilPoller(t *testing.T) {
	h := &Handlers{}
	if owner := h.resolveNodeOwner("device1"); owner != "" {
		t.Errorf("expected empty owner with nil poller, got %q", owner)
	}
}
```

Run it — it will fail because `resolveNodeOwner` doesn't exist yet:

```bash
cd backend && go test ./internal/handlers/ -run TestResolveNodeOwner -v
```

Expected: compile error (`h.resolveNodeOwner undefined`)

- [ ] **Step 2: Add `resolveNodeOwner` to `handlers.go`**

Add after `resolveNodeID` (line 173):

```go
// resolveNodeOwner returns the owner email for a node ID or IP using the device cache.
// Returns an empty string if the node is unknown or has no owner.
func (h *Handlers) resolveNodeOwner(nodeIDOrIP string) string {
	if h.poller == nil {
		return ""
	}
	cache := h.poller.GetDeviceCache()

	var entry *services.DeviceCacheEntry
	if entry = cache.GetDevice(nodeIDOrIP); entry == nil {
		entry = cache.GetDeviceByIP(nodeIDOrIP)
	}
	if entry == nil {
		return ""
	}
	return entry.Owner
}
```

- [ ] **Step 3: Run tests**

```bash
cd backend && go test ./internal/handlers/ -v
```

Expected: all PASS including `TestResolveNodeOwner_NilPoller`

- [ ] **Step 4: Commit**

```bash
git add backend/internal/handlers/handlers.go backend/internal/handlers/handlers_test.go
git commit -m "feat: add resolveNodeOwner helper"
```

---

## Task 3: Include Owner in Stats API Responses

**Files:**
- Modify: `backend/internal/handlers/stats_handlers.go`

- [ ] **Step 1: Add owner to `GetTopTalkers`**

The current `talkerAccum` struct and accumulation loop in `GetTopTalkers` (around line 134):

```go
type talkerAccum struct {
    displayName string
    txBytes     int64
    rxBytes     int64
    totalBytes  int64
}
merged := make(map[string]*talkerAccum)
for _, t := range talkers {
    resolvedID := h.resolveNodeID(t.NodeID)
    name := h.resolveNodeName(resolvedID)
    if name == "" {
        continue
    }
    if existing, ok := merged[resolvedID]; ok {
        existing.txBytes += t.TxBytes
        existing.rxBytes += t.RxBytes
        existing.totalBytes += t.TotalBytes
    } else {
        merged[resolvedID] = &talkerAccum{
            displayName: name,
            txBytes:     t.TxBytes,
            rxBytes:     t.RxBytes,
            totalBytes:  t.TotalBytes,
        }
    }
}
```

Change to:

```go
type talkerAccum struct {
    displayName string
    owner       string
    txBytes     int64
    rxBytes     int64
    totalBytes  int64
}
merged := make(map[string]*talkerAccum)
for _, t := range talkers {
    resolvedID := h.resolveNodeID(t.NodeID)
    name := h.resolveNodeName(resolvedID)
    if name == "" {
        continue
    }
    if existing, ok := merged[resolvedID]; ok {
        existing.txBytes += t.TxBytes
        existing.rxBytes += t.RxBytes
        existing.totalBytes += t.TotalBytes
    } else {
        merged[resolvedID] = &talkerAccum{
            displayName: name,
            owner:       h.resolveNodeOwner(resolvedID),
            txBytes:     t.TxBytes,
            rxBytes:     t.RxBytes,
            totalBytes:  t.TotalBytes,
        }
    }
}
```

And update the enriched slice construction (around line 161):

```go
enriched = append(enriched, gin.H{
    "nodeId":      id,
    "displayName": acc.displayName,
    "owner":       acc.owner,
    "txBytes":     acc.txBytes,
    "rxBytes":     acc.rxBytes,
    "totalBytes":  acc.totalBytes,
})
```

- [ ] **Step 2: Add owner to `GetTopPairs`**

The current `pairAccum` struct and accumulation loop in `GetTopPairs` (around line 222):

```go
type pairAccum struct {
    srcName    string
    dstName    string
    txBytes    int64
    rxBytes    int64
    totalBytes int64
    flowCount  int64
}
pairMerged := make(map[pairKey]*pairAccum)
for _, p := range pairs {
    srcID := h.resolveNodeID(p.SrcNodeID)
    dstID := h.resolveNodeID(p.DstNodeID)
    srcName := h.resolveNodeName(srcID)
    dstName := h.resolveNodeName(dstID)
    if srcName == "" || dstName == "" {
        continue
    }
    key := pairKey{srcID, dstID}
    if existing, ok := pairMerged[key]; ok {
        existing.txBytes += p.TxBytes
        existing.rxBytes += p.RxBytes
        existing.totalBytes += p.TotalBytes
        existing.flowCount += p.FlowCount
    } else {
        pairMerged[key] = &pairAccum{
            srcName:    srcName,
            dstName:    dstName,
            txBytes:    p.TxBytes,
            rxBytes:    p.RxBytes,
            totalBytes: p.TotalBytes,
            flowCount:  p.FlowCount,
        }
    }
}
```

Change to:

```go
type pairAccum struct {
    srcName    string
    srcOwner   string
    dstName    string
    dstOwner   string
    txBytes    int64
    rxBytes    int64
    totalBytes int64
    flowCount  int64
}
pairMerged := make(map[pairKey]*pairAccum)
for _, p := range pairs {
    srcID := h.resolveNodeID(p.SrcNodeID)
    dstID := h.resolveNodeID(p.DstNodeID)
    srcName := h.resolveNodeName(srcID)
    dstName := h.resolveNodeName(dstID)
    if srcName == "" || dstName == "" {
        continue
    }
    key := pairKey{srcID, dstID}
    if existing, ok := pairMerged[key]; ok {
        existing.txBytes += p.TxBytes
        existing.rxBytes += p.RxBytes
        existing.totalBytes += p.TotalBytes
        existing.flowCount += p.FlowCount
    } else {
        pairMerged[key] = &pairAccum{
            srcName:    srcName,
            srcOwner:   h.resolveNodeOwner(srcID),
            dstName:    dstName,
            dstOwner:   h.resolveNodeOwner(dstID),
            txBytes:    p.TxBytes,
            rxBytes:    p.RxBytes,
            totalBytes: p.TotalBytes,
            flowCount:  p.FlowCount,
        }
    }
}
```

And update the enriched slice construction (around line 257):

```go
enriched = append(enriched, gin.H{
    "srcNodeId":      key.src,
    "srcDisplayName": acc.srcName,
    "srcOwner":       acc.srcOwner,
    "dstNodeId":      key.dst,
    "dstDisplayName": acc.dstName,
    "dstOwner":       acc.dstOwner,
    "txBytes":        acc.txBytes,
    "rxBytes":        acc.rxBytes,
    "totalBytes":     acc.totalBytes,
    "flowCount":      acc.flowCount,
})
```

- [ ] **Step 3: Build to verify no compile errors**

```bash
cd backend && go build ./...
```

Expected: no errors

- [ ] **Step 4: Manual smoke test** (requires running backend)

```bash
curl "http://localhost:8080/api/stats/top-talkers?start=2025-01-01T00:00:00Z&end=2026-01-01T00:00:00Z" | jq '.[0]'
```

Expected: response includes an `"owner"` key (may be empty string for external IPs)

- [ ] **Step 5: Commit**

```bash
git add backend/internal/handlers/stats_handlers.go
git commit -m "feat: include device owner in top-talkers and top-pairs API responses"
```

---

## Task 4: Remove liveEmpty Fallback + Fix Analytics Default

**Files:**
- Modify: `frontend/src/lib/stores/stats-store.ts`
- Modify: `frontend/src/routes/analytics/+page.svelte`

- [ ] **Step 1: Remove the liveEmpty fallback from `stats-store.ts`**

Delete lines 117–139 in `stats-store.ts`. The block to remove starts right after `if (signal.aborted) return;` and before `statsState.set(...)`:

```typescript
// DELETE this entire block:
// If live mode returned empty stats, fall back to stored data range
const liveEmpty = ds.mode !== 'historical'
    && (!overviewRes.summary || overviewRes.summary.totalFlows === 0)
    && (!talkersRes.talkers || talkersRes.talkers.length === 0);

if (liveEmpty) {
    if (!ds.dataRange) {
        await dataSourceStore.fetchDataRange();
        ds = get(dataSourceStore);
    }
    if (ds.dataRange && ds.dataRange.count > 0) {
        const rangeStart = new Date(ds.dataRange.earliest);
        const rangeEnd = new Date(ds.dataRange.latest);
        if (rangeStart.getFullYear() > 1970 && rangeEnd > rangeStart) {
            [overviewRes, talkersRes, pairsRes] = await Promise.all([
                tailscaleService.getStatsOverview(rangeStart, rangeEnd, signal),
                tailscaleService.getTopTalkers(rangeStart, rangeEnd, 15, signal),
                tailscaleService.getTopPairs(rangeStart, rangeEnd, 15, signal)
            ]);
            if (signal.aborted) return;
        }
    }
}
```

After deletion, `loadStats` should go directly from the three parallel `await Promise.all(...)` calls to `statsState.set(...)`.

- [ ] **Step 2: Update analytics page — default to 1h, import timeRangeStore and hasHistoricalData**

In `frontend/src/routes/analytics/+page.svelte`, update the store import block (currently importing from `$lib/stores`):

```typescript
import {
    startStatsRefresh,
    stopStatsRefresh,
    statsSummary,
    statsBuckets,
    topTalkers,
    topPairs,
    topPorts,
    statsLoading,
    statsError,
    queryTimeWindow,
    timeRangeStore,
    hasHistoricalData,
    dataSourceStore
} from '$lib/stores';
```

Update `onMount` to default to `'1h'` if the user hasn't explicitly chosen a longer window:

```typescript
onMount(() => {
    // Analytics is most useful over at least 1h; nudge the default up from 5m
    const SHORT_RANGES = new Set(['1m', '5m', '15m', '30m']);
    if (SHORT_RANGES.has($timeRangeStore.selected)) {
        timeRangeStore.setPreset('1h');
    }
    startStatsRefresh(60_000);
});
```

- [ ] **Step 3: Add empty-state banner for when stats are zero but historical data exists**

In `analytics/+page.svelte`, inside the `{:else}` block (after the loading/error checks, before the overview cards), add:

```svelte
{#if $statsSummary && $statsSummary.totalFlows === 0 && $hasHistoricalData && $dataSourceStore.mode !== 'historical'}
    <div class="mb-4 rounded-lg border border-border bg-card px-4 py-3 text-sm text-muted-foreground sm:mb-6">
        No traffic data in the selected window.
        Switch to <button
            class="underline hover:text-foreground"
            onclick={() => dataSourceStore.setMode('historical')}
        >Historical mode</button> to browse stored data.
    </div>
{/if}
```

Place it at line ~150, right before the `<!-- Overview Cards -->` comment.

- [ ] **Step 4: Check TypeScript compiles cleanly**

```bash
cd frontend && npx svelte-check --tsconfig tsconfig.json 2>&1 | tail -20
```

Expected: 0 errors

- [ ] **Step 5: Manual verify**

Start the dev server (`npm run dev` in `frontend/`), open the analytics page, confirm:
- The time window label shows "Last 1h" on first visit (not "Last 5 min")
- Stats stay stable across multiple 60s refreshes (no GB→MB oscillation)
- If the selected window has no data and historical data exists, the banner appears

- [ ] **Step 6: Commit**

```bash
git add frontend/src/lib/stores/stats-store.ts frontend/src/routes/analytics/+page.svelte
git commit -m "fix: remove liveEmpty fallback and default analytics to 1h"
```

---

## Task 5: Add Owner Fields to Frontend Types

**Files:**
- Modify: `frontend/src/lib/types/index.ts`

- [ ] **Step 1: Add owner fields**

Find `TopTalker` (around line 145) and `TopPair` (around line 153) in `types/index.ts`.

Current `TopTalker`:
```typescript
export interface TopTalker {
  nodeId: string;
  displayName?: string;
  txBytes: number;
  rxBytes: number;
  totalBytes: number;
}
```

Change to:
```typescript
export interface TopTalker {
  nodeId: string;
  displayName?: string;
  owner?: string;
  txBytes: number;
  rxBytes: number;
  totalBytes: number;
}
```

Current `TopPair`:
```typescript
export interface TopPair {
  srcNodeId: string;
  srcDisplayName?: string;
  dstNodeId: string;
  dstDisplayName?: string;
  txBytes: number;
  rxBytes: number;
  totalBytes: number;
  flowCount: number;
}
```

Change to:
```typescript
export interface TopPair {
  srcNodeId: string;
  srcDisplayName?: string;
  srcOwner?: string;
  dstNodeId: string;
  dstDisplayName?: string;
  dstOwner?: string;
  txBytes: number;
  rxBytes: number;
  totalBytes: number;
  flowCount: number;
}
```

- [ ] **Step 2: Type check**

```bash
cd frontend && npx svelte-check --tsconfig tsconfig.json 2>&1 | tail -20
```

Expected: 0 errors

- [ ] **Step 3: Commit**

```bash
git add frontend/src/lib/types/index.ts
git commit -m "feat: add owner fields to TopTalker and TopPair types"
```

---

## Task 6: Add Owner Columns to Analytics Tables

**Files:**
- Modify: `frontend/src/routes/analytics/+page.svelte`

### Top Talkers — add Owner column

- [ ] **Step 1: Add Owner header to the desktop table**

The current `<thead>` for Top Talkers ends with the Total column header (around line 235). Add an Owner column header **between Device and TX**:

```svelte
<th class="pb-2 pr-4">Device</th>
<th class="pb-2 pr-4 text-muted-foreground">Owner</th>   <!-- ADD THIS -->
<th
    class="cursor-pointer select-none pb-2 pr-4 text-right ..."
    onclick={() => toggleTalkerSort('txBytes')}
    ...
>
    TX{sortArrow(talkerSort === 'txBytes', talkerSortDir)}
</th>
```

- [ ] **Step 2: Add Owner cell to each talker row**

In the `{#each sortedTalkers ...}` loop, after the Device `<td>` (around line 247), add:

```svelte
<td class="max-w-[160px] truncate py-1.5 pr-4 text-xs text-muted-foreground" title={talker.owner ?? ''}>
    {talker.owner ?? '—'}
</td>
```

- [ ] **Step 3: Add owner to mobile card view**

In the mobile view for Top Talkers (inside `class="divide-y divide-border/50 sm:hidden"`), add the owner below the TX/RX line. Find the `<div class="mt-0.5 flex gap-3 pl-5 ...">` line and add below it:

```svelte
{#if talker.owner}
    <div class="mt-0.5 truncate pl-5 text-xs text-muted-foreground/70">{talker.owner}</div>
{/if}
```

### Top Pairs — stack owner below device name

- [ ] **Step 4: Stack owner under Source device name in desktop table**

In the Top Pairs desktop table, find the Source `<td>` (around line 340):

```svelte
<td class="max-w-[140px] truncate py-1.5 pr-4" title={pair.srcNodeId}>
    {#if pair.srcDisplayName}
        <span class="font-medium">{pair.srcDisplayName}</span>
    {:else}
        <span class="font-mono text-xs text-muted-foreground">
            {nodeLabel(pair.srcNodeId)}
        </span>
    {/if}
</td>
```

Change to (remove `truncate` from `<td>`, add owner stacked below):

```svelte
<td class="max-w-[140px] py-1.5 pr-4" title={pair.srcNodeId}>
    <div class="truncate">
        {#if pair.srcDisplayName}
            <span class="font-medium">{pair.srcDisplayName}</span>
        {:else}
            <span class="font-mono text-xs text-muted-foreground">
                {nodeLabel(pair.srcNodeId)}
            </span>
        {/if}
    </div>
    {#if pair.srcOwner}
        <div class="truncate text-xs text-muted-foreground/70">{pair.srcOwner}</div>
    {/if}
</td>
```

- [ ] **Step 5: Stack owner under Destination device name in desktop table**

Apply the same pattern to the Destination `<td>` (around line 352):

```svelte
<td class="max-w-[140px] py-1.5 pr-4" title={pair.dstNodeId}>
    <div class="truncate">
        {#if pair.dstDisplayName}
            <span class="font-medium">{pair.dstDisplayName}</span>
        {:else}
            <span class="font-mono text-xs text-muted-foreground">
                {nodeLabel(pair.dstNodeId)}
            </span>
        {/if}
    </div>
    {#if pair.dstOwner}
        <div class="truncate text-xs text-muted-foreground/70">{pair.dstOwner}</div>
    {/if}
</td>
```

- [ ] **Step 6: Add owners to mobile pairs view**

In the mobile pairs view, find the `<div class="mt-0.5 flex items-center gap-1 text-xs">` line and add owner info below it:

```svelte
{#if pair.srcOwner || pair.dstOwner}
    <div class="mt-0.5 flex items-center gap-1 text-[10px] text-muted-foreground/70">
        <span class="truncate">{pair.srcOwner ?? '—'}</span>
        <span class="shrink-0">&rarr;</span>
        <span class="truncate">{pair.dstOwner ?? '—'}</span>
    </div>
{/if}
```

- [ ] **Step 7: Type check**

```bash
cd frontend && npx svelte-check --tsconfig tsconfig.json 2>&1 | tail -20
```

Expected: 0 errors

- [ ] **Step 8: Visual verify in browser**

Start dev server (`npm run dev`). On the analytics page:
- Top Talkers desktop table has an Owner column between Device and TX showing emails or `—`
- Top Pairs desktop table shows owner in smaller muted text below each device name
- Mobile views show owner info
- Empty/external-IP entries show `—` (talkers) or nothing (pairs, since the `{#if}` hides it)

- [ ] **Step 9: Commit**

```bash
git add frontend/src/routes/analytics/+page.svelte
git commit -m "feat: add device owner to analytics Top Talkers and Top Pairs tables"
```
