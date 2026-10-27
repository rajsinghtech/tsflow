# Changelog

## v1.7.0 (unreleased)

Upgrading from 1.6 needs no config changes. Read [Upgrading from 1.6](README.md#upgrading-from-16) for the few things that look different.

### New

- Several tailnets from one tsflow with `TSFLOW_TAILNETS_FILE`, a header switcher, and a `tailnet` query parameter on data routes (#373, #377, #381, #389)
- Per-tailnet flow log sources: API, S3 or GCS per entry (#393)
- Opt-in workload identity for the Tailscale API and the flow log bucket, plus file-based tsnet tokens (#378, #385, #399)
- Native GCS backend with Application Default Credentials (#378)
- Opt-in access control for internal deploys: tsnet WhoIs or a trusted proxy, capability or group grants (#386, #392, #398)
- Analytics page with ranked talkers and pairs, search, paging and owners (#396, #397)
- Filter the graph and analytics by tag and login (#405)
- New connections: src/dst pairs first seen in the selected window, in the API, MCP and the Me tab (#407)
- Per-device timeline with top peers (#408)
- Me tab for the signed-in viewer (#410)
- Time range in the header and in the URL, with free-text ranges like `last 3 hours` (#415)
- Opt-in read-only MCP server at `/mcp` (#406, #432, #435, #439)
- MCP: relative time ranges like `now-2h` and `last 7d`, a `whoami` tool, week-long rankings that fit the query timeout at 20k devices, and setup docs for Claude Code, Claude Desktop and Cursor (#478, #479, #480, #481)
- Opt-in Prometheus metrics at `/metrics` on their own listener with `TSFLOW_METRICS_ADDR`: poller freshness and errors, ingest, DB size, HTTP and MCP latency (#533)
- `TSFLOW_HEALTH_PORT` for Kubernetes probes in tsnet mode (#399)
- `TSFLOW_TRUSTED_PROXIES` to say which proxies may set the client address (#421)
- Main-branch images at `ghcr.io/rajsinghtech/tsflow:main` (#394)

### Faster

- Hourly rollups for wide graph and stats reads (#390)
- Graph reads in one pass, read in parallel, with closed hours cached in memory (`TSFLOW_GRAPH_CACHE_MB`) (#375, #440, #442, #443)
- Faster device lookups, cache merges and large graph layouts (#371, #387, #413)
- Smaller per-pair protocol and port sums (#441)
- Skip stats scans that are already covered (#376)

### Fixed

- Stats totals, rankings and bounded log reads (#404)
- Active device counts and network log RX bytes (#403)
- Search by user login, and no duplicate devices from flow metadata (#402)
- Late API log messages are no longer dropped: polls stay 2 minutes behind now (#436)
- Flows from nodes with a clock running ahead are stored at their logged time (#419)
- Implausible flow counters are rejected so window sums can't overflow (#409)
- Exit-node traffic with a blanked public side is kept (#426)
- Reply bytes are filed under the service port (#431)
- Bandwidth rates use the store's bucket width (#425)
- Only 100.64.0.0/10 counts as Tailscale, and all of fc00::/7 as private (#428)
- Unreadable flow objects are skipped after five polls (#416)
- Poller errors clear after a good poll, and empty polls show in the status (#417, #427, #447)
- Device list tags win over flow-log snapshots (#418)
- Live graph windows read the filling hour correctly (#437, #438, #444)
- Bare-number protocol keys no longer fail traffic stats (#446)
- Tailscale API 403 and 404 error bodies are kept (#401)
- Policy errors are shown, and the policy panel stays off the zoom controls (#423, #424)
- Header shows a placeholder until totals load, and null device lists are handled (#422, #430)
- IP set ranges are readable in dark mode (#369)

### Security

- `X-Forwarded-For` is trusted only from configured proxies (#412, #421)
- No CORS origin reflection in development mode (#414)
- Header and idle timeouts on every listener (#429)
- SPA trailing-slash redirects stay on the same site (#411)
- Invalid rate limiter settings fall back to defaults (#434)

### Behavior changes

- Physical traffic is left out of totals unless selected (#404)
- `/api/network-logs` serves at most 30 minutes per request (#404)
- Device `id` is the stable node id; numeric ids still work in queries (#402)
- Rate limit is keyed by the TCP peer unless `TSFLOW_TRUSTED_PROXIES` is set (#421)

### Upgrade

- The database migrates on first start and writes `<db>.pre-tailnet` first, for rollback (#368)
- Every 1.6.2 config shape is covered by a test against the current loader
- [docs/environment.md](docs/environment.md) lists every variable, kept in sync by a test

### UI polish

- The Me tab works with the API flow source: device list identities are stored (#458)
- Network Logs list each pair once (#463)
- Network Logs sort headers work from the keyboard (#465)
- A device timeline counts every id of the device (#466)
- Screen readers name graph cards and edges by hostname (#467)
- Escape closes the time range picker (#468)
- More common service ports are named (#470)
- Every device timeline column fits in the chart (#472)
- Dense graphs still render: the slow layout stops at a time budget (#461)
- A crashed graph layout retries fast instead of freezing the tab (#473)
- The Traffic header count is labeled pairs (#471)
- Empty ranges say when the poller can't fetch flow logs (#464)
- The New tab is gone; old `/new` links open Analytics with the same query string

### Mobile

- The header and time bar fit on phones (#475)
- Phones can hide the Traffic panels, and device details open in a sheet over the graph (#474)
- The graph stays on screen when a device is selected (#476)
- Analytics, New and Me fit phones, with cards for new connections and 44px touch targets (#477)
- The policy graph gets the whole screen on phones (#469)

### Dependencies

- SvelteKit, Vite and the static adapter upgraded together (#370)
- Go module and npm updates from Renovate, including klauspost/compress 1.20.3 (#462, #483)
