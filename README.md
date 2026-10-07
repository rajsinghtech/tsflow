# TSFlow - Tailscale Network Flow Visualizer

A real-time network traffic visualization dashboard for Tailscale networks. Monitor device connectivity, analyze bandwidth usage, and explore network flows with an interactive graph interface.

## Installation

### Homebrew (macOS/Linux)

```bash
brew install rajsinghtech/tap/tsflow
```

### Docker

```bash
docker pull ghcr.io/rajsinghtech/tsflow:latest
```

`ghcr.io/rajsinghtech/tsflow:main` tracks main and isn't a release.

### Binary Download

Download from [GitHub Releases](https://github.com/rajsinghtech/tsflow/releases).

## Quick Start

> **Note:** TSFlow requires **Tailscale Network Flow Logs** (Premium/Enterprise plans). Enable it in your [Tailscale admin console](https://login.tailscale.com/admin/logs).

### Run with Homebrew

```bash
export TAILSCALE_OAUTH_CLIENT_ID=your-client-id
export TAILSCALE_OAUTH_CLIENT_SECRET=your-client-secret
tsflow
```

Open `http://localhost:8080`

### Run with Docker

```bash
docker run -d \
  --name tsflow \
  -p 8080:8080 \
  -v tsflow_data:/app/data \
  -e TAILSCALE_OAUTH_CLIENT_ID=your-client-id \
  -e TAILSCALE_OAUTH_CLIENT_SECRET=your-client-secret \
  ghcr.io/rajsinghtech/tsflow:latest
```

## Configuration

### Authentication

TSFlow supports OAuth (recommended) or API key authentication.

**OAuth Setup:**
1. Go to [OAuth clients](https://login.tailscale.com/admin/settings/oauth) in Tailscale Admin
2. Create a new OAuth client with `all:read` scope
3. Set `TAILSCALE_OAUTH_CLIENT_ID` and `TAILSCALE_OAUTH_CLIENT_SECRET`

**API Key Setup:**
1. Go to [API keys](https://login.tailscale.com/admin/settings/keys) in Tailscale Admin
2. Create a new API key
3. Set `TAILSCALE_API_KEY`

### Several tailnets

The single-tailnet environment variables configure one tailnet with id `default`. To watch more than one, leave `TAILSCALE_TAILNET`, `TAILSCALE_API_KEY`, and the OAuth client variables unset, and set `TSFLOW_TAILNETS_FILE` to a YAML or JSON file. Secrets are not written in the file. Each entry points at an environment variable or a file.

Data routes take an optional `tailnet` query parameter. With one configured tailnet the parameter can be omitted, and the JSON matches a single-tailnet install. With several tailnets, a missing parameter uses id `default` when that id is configured. If it is not, the response is 400 and lists the valid ids. An unknown id is 404. `GET /api/tailnets` returns each id, display name, and poller status, including the last error. It does not return credentials. The UI calls `GET /api/tailnets` once on load. With one tailnet it shows no switcher and leaves data requests unchanged. With several, a header switcher keeps the choice in the `tailnet` query parameter and sends that id on data requests.

```yaml
tailnets:
  - id: default
    tailnet: example.com
    api_key_env: DEFAULT_TAILSCALE_API_KEY
    s3_prefix: network/
  - id: lab
    tailnet: lab.example.com
    oauth_client_id_file: /secrets/lab/client-id
    oauth_client_secret_file: /secrets/lab/client-secret
    s3_prefix: lab/network/
```

`s3_prefix` is optional. When it is omitted, the tailnet uses `TSFLOW_S3_PREFIX`. `prefix` is the same field. `api_url` and `oauth_scopes` are optional too. `oauth_scopes` is a comma-separated string or a list. An entry needs either `api_key_env` or `api_key_file`, or both OAuth client id and secret. Do not set both an environment variable and a file for the same secret. An `auth` block (`type: oauth`, `api_key`, or `wif`) is the other way to set credentials. Do not combine it with the flat credential fields. See [docs/workload-identity.md](docs/workload-identity.md).

An entry can also say where its flow logs live. `flow_backend` is `api`, `s3`, or `gcs`. `bucket`, `region`, `endpoint`, and `s3_auth` (`static`, `aws_default`, or `gcs_adc`) override the matching process settings. A field that is left out inherits the process value, so existing files that only set `s3_prefix` stay valid. `role_arn` with `web_identity_token_file` assumes that AWS role from the OIDC token in the file (`AssumeRoleWithWebIdentity`). That client does not read `AWS_ROLE_ARN` or `AWS_WEB_IDENTITY_TOKEN_FILE`, so two tailnets can use two token files. Static object-store keys stay on the process.

```yaml
tailnets:
  - id: prod
    tailnet: prod.example.com
    api_key_env: PROD_TAILSCALE_API_KEY
    flow_backend: gcs
    bucket: example-prod-flow-logs
    prefix: network/
  - id: staging
    tailnet: staging.example.com
    api_key_env: STAGING_TAILSCALE_API_KEY
    flow_backend: s3
    s3_auth: aws_default
    bucket: example-staging-flow-logs
    region: us-east-1
    role_arn: arn:aws:iam::123456789012:role/tsflow-reader
    web_identity_token_file: /var/run/tsflow/staging/gcp-token
    prefix: staging/network/
  - id: lab
    tailnet: lab.example.com
    api_key_env: LAB_TAILSCALE_API_KEY
    flow_backend: api
```

`prod` reads a GCS bucket with Application Default Credentials. `staging` assumes a role in another account with a Google-issued ID token. `lab` has no streaming bucket and uses the Tailscale API logs endpoint.

JSON uses the same fields:

```json
{"tailnets":[{"id":"default","tailnet":"example.com","api_key_env":"DEFAULT_TAILSCALE_API_KEY"}]}
```

### Environment Variables

#### Tailscale Authentication

| Variable | Description | Default |
|----------|-------------|---------|
| `TAILSCALE_OAUTH_CLIENT_ID` | OAuth client ID | - |
| `TAILSCALE_OAUTH_CLIENT_SECRET` | OAuth client secret | - |
| `TAILSCALE_OAUTH_SCOPES` | OAuth scopes (comma-separated) | `all:read` |
| `TAILSCALE_API_KEY` | API key (alternative to OAuth) | - |
| `TAILSCALE_AUTH` | `oauth`, `api_key`, or `wif`. Unset keeps the historical choice. | - |
| `TAILSCALE_WIF_CLIENT_ID` | Federated client id for API calls. Requires `TAILSCALE_AUTH=wif`. | - |
| `TAILSCALE_WIF_ID_TOKEN` | OIDC token for API workload identity. One token source only. | - |
| `TAILSCALE_WIF_ID_TOKEN_FILE` | File containing that OIDC token. | - |
| `TAILSCALE_WIF_AUDIENCE` | Audience used to request a platform token when no token is supplied. | - |
| `TAILSCALE_TAILNET` | Tailnet name (`-` for auto-detect) | `-` |
| `TAILSCALE_API_URL` | API endpoint | `https://api.tailscale.com` |
| `TSFLOW_TAILNETS_FILE` | YAML or JSON list of tailnets. Do not combine with the single-tailnet variables above. | - |

#### Server Settings

| Variable | Description | Default |
|----------|-------------|---------|
| `PORT` | Server port | `8080` |
| `ENVIRONMENT` | `development` or `production` | `development` |
| `ALLOWED_CORS_ORIGINS` | Comma-separated origins allowed to call the API cross-origin. Unset, production allows none and development allows only loopback origins such as `http://localhost:3000` | unset |
| `TSFLOW_MCP_ENABLED` | Serve a read-only MCP endpoint at `/mcp`. Off unless set to `true` or `1`. | `false` |

#### tsnet Serve Mode

TSFlow can embed a Tailscale node and serve itself directly on your tailnet, eliminating the need for a separate Tailscale sidecar container.

| Variable | Description | Default |
|----------|-------------|---------|
| `TSFLOW_SERVE` | Enable tsnet serve mode | `false` |
| `TSFLOW_HOSTNAME` | MagicDNS hostname on the tailnet | `tsflow` |
| `TSFLOW_TAGS` | Comma-separated ACL tags (e.g. `tag:tsflow`) | - |
| `TSFLOW_FUNNEL` | Expose via Tailscale Funnel. Refused when more than one tailnet is configured. | `false` |
| `TSFLOW_STATE_DIR` | tsnet state persistence directory | `./data/tsnet-state` |
| `TSFLOW_HEALTH_PORT` | Also serve `GET /health`, and nothing else, on this plain port. For Kubernetes probes, which cannot reach the tailnet. Only valid with `TSFLOW_SERVE=true`. | - |

##### Workload Identity Federation

tsnet mode supports [workload identity federation](https://tailscale.com/kb/1236/workload-identity) as an alternative to OAuth secrets. This lets tsflow authenticate using platform identity (AWS, GCP, GitHub Actions, Azure) without managing secrets.

| Variable | Description | Default |
|----------|-------------|---------|
| `TS_CLIENT_ID` | Federated client ID | - |
| `TS_ID_TOKEN` | ID token from identity provider | - |
| `TS_ID_TOKEN_FILE` | File holding the ID token, read once at startup. For tokens that a sidecar or init container writes. | - |
| `TS_AUDIENCE` | Audience for requesting platform tokens | - |

When `TS_CLIENT_ID` is set, tsflow uses WIF instead of OAuth `ClientSecret` for the tsnet node. The platform token is auto-detected from the runtime environment. Set exactly one of `TS_ID_TOKEN`, `TS_ID_TOKEN_FILE`, or `TS_AUDIENCE`. You must also set `TSFLOW_TAGS`.

tsnet WIF registers the embedded node only. API calls and the flow-log bucket have their own opt-in modes, described in [docs/workload-identity.md](docs/workload-identity.md). `TAILSCALE_AUTH=wif` is the API mode. `TSFLOW_S3_AUTH=aws_default` is the bucket mode. Leaving both unset keeps OAuth, API key, and static S3 keys.

**Requirements:**
- OAuth credentials or workload identity federation (API keys are not supported in tsnet mode)
- ACL tags must be allowed for the OAuth client or federated identity to register nodes
- For Funnel, the ACL must grant funnel access to the tag

**tsnet mode serves on both port 80 (HTTP) and port 443 (HTTPS).**

**Example with OAuth:**

```bash
docker run -d \
  --name tsflow \
  -v tsflow_data:/app/data \
  -e TAILSCALE_OAUTH_CLIENT_ID=your-client-id \
  -e TAILSCALE_OAUTH_CLIENT_SECRET=your-client-secret \
  -e TSFLOW_SERVE=true \
  -e TSFLOW_HOSTNAME=tsflow \
  -e TSFLOW_TAGS=tag:tsflow \
  ghcr.io/rajsinghtech/tsflow:latest
```

**Example with Workload Identity (GCP):**

```bash
docker run -d \
  --name tsflow \
  -v tsflow_data:/app/data \
  -e TAILSCALE_OAUTH_CLIENT_ID=your-client-id \
  -e TAILSCALE_OAUTH_CLIENT_SECRET=your-client-secret \
  -e TSFLOW_SERVE=true \
  -e TSFLOW_HOSTNAME=tsflow \
  -e TSFLOW_TAGS=tag:tsflow \
  -e TS_CLIENT_ID=your-federated-client-id \
  -e TS_AUDIENCE=your-tailnet.org \
  ghcr.io/rajsinghtech/tsflow:latest
```

TSFlow will be accessible at both `https://tsflow.<your-tailnet>.ts.net` and `http://tsflow.<your-tailnet>.ts.net`.

#### Internal access control

Access control is off unless you set it. With the variables below unset, every request behaves as it does today, including `GET /api/tailnets` and the data routes. Health checks stay open either way: `GET /health` and `GET /api/health`.

When it is on, a viewer needs a Tailscale application capability, or membership in a mapped group, unless you set `TSFLOW_ACCESS_GRANTS=identity`. That setting is explicit: any resolved WhoIs identity, or a `Tailscale-User-Login` header from a trusted proxy, can see every configured tailnet, and no identity is 403. whoami and autoscope still work. Unset, or `required`, still requires a capability or a group grant, including when `TSFLOW_ACCESS_MODE` is set. The capability name is `TSFLOW_ACCESS_CAPABILITY`. The example below uses `example.com/cap/tsflow`. Each grant is a JSON object. The only field is optional:

```json
{"tailnets": ["default", "lab"]}
```

Omit `tailnets`, or include `"*"`, to allow every configured tailnet id. Those ids are the ones from `TSFLOW_TAILNETS_FILE` (`default` when you use the single-tailnet environment variables). A request with no matching capability and no mapped group is `403` with `missing access grant`. `GET /api/tailnets` lists only the tailnets that grant allows. Other data routes return `403` for a tailnet the grant does not include. If several grants match, the tailnet lists are unioned. One grant that allows every tailnet allows every tailnet.

Identity is logged per request only when `TSFLOW_LOG_LEVEL=debug`.

A denied request always logs one line with the reason, the TCP peer (`peer=`, the `r.RemoteAddr` that trusted-proxy checks use), the raw `X-Forwarded-For` value, and in header mode whether the peer matched `TSFLOW_ACCESS_TRUSTED_PROXIES` (`trusted_proxy=`). The client IP in the request log comes from `X-Forwarded-For`, so it is not the peer. Each peer and reason pair is logged at most once a minute, or on every request with `TSFLOW_LOG_LEVEL=debug`. Identity headers are not logged.

There are two front doors.

**tsnet.** `TSFLOW_SERVE=true` with `TSFLOW_ACCESS_CAPABILITY` set. tsflow is already a node on the tailnet. Each request calls LocalAPI WhoIs on the connection's remote address and reads that peer's capability map. You can leave `TSFLOW_ACCESS_MODE` unset. Set it to `tsnet` if you want to be explicit. `TSFLOW_SERVE` must be true for that mode. Do not enable Funnel for this, because Funnel traffic has no tailnet identity and those requests are denied.

Example grant in the tailnet policy file:

```hujson
{
  "grants": [
    {
      "src": ["group:eng"],
      "dst": ["tag:tsflow"],
      "app": {
        "example.com/cap/tsflow": [
          {"tailnets": ["default", "lab"]}
        ]
      }
    },
    {
      "src": ["group:ops"],
      "dst": ["tag:tsflow"],
      "app": {
        "example.com/cap/tsflow": [{}]
      }
    }
  ]
}
```

The empty object allows every configured tailnet. `group:ops` can see all of them. `group:eng` can see `default` and `lab`.

**Reverse proxy.** Use this when Tailscale Serve, or Caddy with the Tailscale plugin, already authenticated the viewer. Set `TSFLOW_ACCESS_MODE=header` and `TSFLOW_ACCESS_TRUSTED_PROXIES` to the proxy CIDRs. Identity headers are trusted only from those CIDRs. If the proxy dials localhost over IPv6, include `::1/128` next to `127.0.0.1/32`. The same rule applies to the groups header. A client that is not in the list cannot supply `Tailscale-User-Login`, `Tailscale-User-Name`, the capability header, or the groups header. Those values are ignored, and the request is denied.

Tailscale Serve sends `Tailscale-User-Login` and `Tailscale-User-Name`. If your proxy sends identity under other names, set `TSFLOW_ACCESS_USER_HEADER` (login, for example `X-Tailscale-User`) and `TSFLOW_ACCESS_NAME_HEADER` (display name, for example `X-Tailscale-Name`). Point them at headers the proxy always overwrites, never at one a client can pass through. A custom header replaces the default for that field, and the default name is not read as a fallback. To also send capabilities, pass `--accept-app-caps` with the same capability name. Serve puts them in `Tailscale-App-Capabilities`. Override that name with `TSFLOW_ACCESS_CAPABILITY_HEADER` if your proxy uses another header.

```bash
tailscale serve --accept-app-caps=example.com/cap/tsflow --https=443 http://127.0.0.1:8080
```

```bash
TSFLOW_ACCESS_MODE=header
TSFLOW_ACCESS_CAPABILITY=example.com/cap/tsflow
TSFLOW_ACCESS_TRUSTED_PROXIES=127.0.0.1/32
```

If a local tailscaled socket is reachable, header mode calls WhoIs on the `X-Forwarded-For` peer instead of trusting the identity and capability headers. The right-most forwarded address, across every `X-Forwarded-For` line, is the peer. If that entry is not an IP address the request is denied. Set `TSFLOW_ACCESS_LOCAL_WHOIS=off` to always trust headers. Set it to `require`, or set `TSFLOW_ACCESS_TAILSCALED_SOCKET`, when a missing socket should stop startup. The default is `auto`: use WhoIs when the socket answers, and headers when it does not.

Some proxies forward identity and a groups header, and do not forward app capabilities. Set `TSFLOW_ACCESS_GROUPS_HEADER` to that header name. Values are comma-separated and must match the keys in the grant map exactly. Load the map from `TSFLOW_ACCESS_GROUP_GRANTS` or `TSFLOW_ACCESS_GROUP_GRANTS_FILE`, not both. The values use the same grant object as the capability. A mapped group grants access. If a request has both a capability and mapped groups, the tailnet lists are unioned.

```json
{"group:eng": {"tailnets": ["default"]}, "group:ops": {}, "ops@example.com": {"tailnets": ["lab"]}}
```

Caddy, using the Tailscale plugin for identity. `tailscale_user` is the full login and `tailscale_name` is the display name. If the proxy also sends a groups header, forward that header under the name you set in `TSFLOW_ACCESS_GROUPS_HEADER`.

```caddyfile
{
  order tailscale_auth before reverse_proxy
}

:443 {
  bind tailscale/tsflow
  tls {
    get_certificate tailscale
  }
  tailscale_auth
  reverse_proxy 127.0.0.1:8080 {
    header_up Tailscale-User-Login {http.auth.user.tailscale_user}
    header_up Tailscale-User-Name {http.auth.user.tailscale_name}
  }
}
```

```bash
TSFLOW_ACCESS_MODE=header
TSFLOW_ACCESS_TRUSTED_PROXIES=127.0.0.1/32
TSFLOW_ACCESS_GROUPS_HEADER=X-Tsflow-Groups
TSFLOW_ACCESS_GROUP_GRANTS={"group:eng":{"tailnets":["default"]},"group:ops":{}}
```

On Kubernetes, keep the Service reachable only from the proxy, and set the trusted CIDR to that proxy range. The default manifests in `k8s/` do not enable this. `k8s/access-example.yaml` is a starting point:

```yaml
env:
  - name: TSFLOW_ACCESS_MODE
    value: header
  - name: TSFLOW_ACCESS_CAPABILITY
    value: example.com/cap/tsflow
  - name: TSFLOW_ACCESS_TRUSTED_PROXIES
    value: 10.0.0.0/8
  - name: TSFLOW_ACCESS_GROUPS_HEADER
    value: X-Tsflow-Groups
  - name: TSFLOW_ACCESS_GROUP_GRANTS
    value: '{"group:eng":{"tailnets":["default"]}}'
  - name: TSFLOW_ACCESS_AUTOSCOPE
    value: user
```

Startup fails when the settings disagree. Header mode without trusted CIDRs is rejected. A capability with neither `TSFLOW_SERVE` nor header mode is rejected. A groups header without a grant map is rejected.

`GET /api/whoami` returns the viewer when a grant matched. With access control off it returns `{"autoscope":"off"}`.

`TSFLOW_ACCESS_AUTOSCOPE` is `off` by default. `user` preselects devices whose user matches the viewer login. `groups` preselects devices for the mapped groups the viewer is in: `group:eng` and `eng` match tag `tag:eng`, and a mapping key that contains `@` matches devices owned by that login. This is only the initial traffic view. The filter panel has a My devices chip with Clear. Clearing it shows the full view. The API does not enforce the device filter.

#### Data Storage & Polling

| Variable | Description | Default |
|----------|-------------|---------|
| `TSFLOW_DB_PATH` | SQLite database path | `./data/tsflow.db` |
| `TSFLOW_SKIP_DB_BACKUP` | Skip the pre-migration database copy. Set `1` only when disk space is tight. | unset |
| `TSFLOW_POLL_INTERVAL` | How often to import new flow logs | `5m` |
| `TSFLOW_INITIAL_BACKFILL` | How far back to fetch logs on startup | `6h` |
| `TSFLOW_RETENTION` | How long to keep flow data. Set `0` to disable cleanup. | `720h` for API mode, disabled for S3 mode |
| `TSFLOW_FLOW_BACKEND` | Flow backend: `api`, `s3`, or `gcs` | `api` |
| `TSFLOW_S3_AUTH` | `static`, `aws_default`, or `gcs_adc` | `static` |
| `TSFLOW_S3_ROLE_ARN` | Optional role to assume when auth is `aws_default`. With a web identity token file this is `AssumeRoleWithWebIdentity`. | - |
| `TSFLOW_S3_WEB_IDENTITY_TOKEN_FILE` | OIDC token file used to assume `TSFLOW_S3_ROLE_ARN`. A tailnets entry can set its own file. | - |
| `TSFLOW_S3_BUCKET` | S3/Garage bucket containing exported flow logs | `tailscale-logs` |
| `TSFLOW_S3_PREFIX` | Object prefix for network flow objects | `network/` |
| `TSFLOW_S3_ENDPOINT` | S3-compatible endpoint URL | - |
| `TSFLOW_S3_REGION` | S3 region | `garage` |
| `TSFLOW_S3_ACCESS_KEY_ID` | S3 access key ID | - |
| `TSFLOW_S3_SECRET_ACCESS_KEY` | S3 secret access key | - |
| `TSFLOW_S3_LOOKBACK` | Object-store listing overlap for late objects | `15m` |
| `TSFLOW_S3_MAX_OBJECTS_PER_POLL` | Max objects imported per poll cycle | `500` |

### Data Storage

TSFlow stores per-minute flow aggregates in SQLite with a rolling retention window (default 30 days). Charts over wider windows use query-time bucketing — no data loss from pre-aggregation. When `TSFLOW_FLOW_BACKEND=s3`, TSFlow imports immutable `network/YYYY/MM/DD/*.ndjson`, `*.ndjson.zst`, `*.ndjson.zstd`, `*.ndjson.gz`, or `*.ndjson.gzip` objects from S3-compatible storage and tracks ingested object keys so repeated polling does not double count traffic.

### Ranked talkers and pairs

`GET /api/analytics/talkers` and `GET /api/analytics/pairs` return JSON rankings for a time window. The Analytics page's Top Talkers and Top Pairs tables use them, for the page's window and traffic-type filter, with previous and next pages, a bytes or flows sort, and a search box. Clicking a device name opens it on the traffic graph over the same window. The `/api/stats/top-talkers` and `/api/stats/top-pairs` routes stay for API callers.

`start` and `end` are RFC3339. `limit` defaults to 20 and stops at 200. `offset` defaults to 0. `sort` is `bytes` (total volume, the default) or `flows`. On a single-tailnet install the `tailnet` parameter can be omitted. With several tailnets, pass the same id the other data routes use. Optional `q` narrows both rankings before paging, with the same rules as the traffic graph search: `tag:x`, `ip:x`, `user@x` for the owner login, or a case-insensitive substring of a device name, owner email, address, or tag. A pair matches when either end does.

A talker is one device. The row has `nodeId`, `hostname`, `owner`, `txBytes`, `rxBytes`, `totalBytes`, and `flowCount`. `nodeId` is the canonical device id, and rows stored under another id for the same device (a legacy numeric id or an address) are merged into it on the page. `hostname` and `owner` come from the device list, falling back to the stored flow-log name; `127.3.3.40` is labeled `DERP relay`. A pair row has `srcNodeId`, `srcHostname`, `srcOwner`, `dstNodeId`, `dstHostname`, `dstOwner`, the same byte fields, and `flowCount`. Physical (WireGuard transport) traffic is left out unless `trafficTypes` includes `physical`. Rows are ordered by the sort field descending, then by id. `metadata.hasMore` is true when a later page exists.

An hour that sits fully inside the window is read from the hourly rollup (`node_pair_hours`). The partial hour at each end is read from minute rows in `node_pairs`. When no hour is rolled up yet, the read uses minute rows. An empty window returns an empty list. Optional `trafficTypes` uses the same values as the other stats routes (`virtual`, `subnet`, `exit`, `physical`).

### MCP server

`TSFLOW_MCP_ENABLED=true` serves a read-only [Model Context Protocol](https://modelcontextprotocol.io) endpoint at `/mcp` on the same HTTP server as the UI. The route is absent when the variable is unset. It uses streamable HTTP and the same access middleware as `/api`: trusted-proxy and WhoIs identity, then the capability or group grant and its optional tailnet allowlist. A viewer cannot query a tailnet outside that allowlist. There is no separate MCP credential.

Identity autoscope is only the initial device view, the same way the UI starts. Device tools take `scope`: `mine` or `all`. When autoscope is `user` or `groups`, the default is `mine` and results match that filter. `scope=all` clears it and returns every device in the permitted tailnets. `list_tailnets` and `stats_overview` stay tailnet-wide, matching the REST routes. The REST API still does not enforce the device filter.

Tools are `list_tailnets`, `search_devices`, `get_device`, `top_talkers`, `top_pairs`, `flows_between`, `device_peers`, `device_timeline`, `new_connections`, and `stats_overview`. They read stored rollups. Physical transport is excluded unless `trafficTypes` includes `physical`. DERP relays are labeled `DERP relay`. Windows default to the last hour and stop at 7 days. List results default to 20 rows and stop at 100.

Claude and Cursor can attach the endpoint as a remote MCP server. Point the client at the tailnet URL when tsflow is served with `TSFLOW_SERVE`, or at the trusted proxy when header access is on. The client does not get a new privilege path.

```json
{
  "mcpServers": {
    "tsflow": {
      "type": "http",
      "url": "https://tsflow.example.ts.net/mcp"
    }
  }
}
```

The repository has Kubernetes manifests, not a Helm chart. Set the variable on the container:

```yaml
env:
  - name: TSFLOW_MCP_ENABLED
    value: "true"
```

`k8s/deployment.yaml` leaves it `false`. `k8s/access-example.yaml` shows it next to the access settings. Keep the Service reachable only from the tailnet or the trusted proxy. The health port used with `TSFLOW_SERVE` does not expose `/mcp`.

Raw flow-log endpoints are deprecated because raw events are not retained: use `/api/flow-logs/aggregated` for historical traffic. The legacy `/api/flow-logs` and `/api/devices/:deviceId/flows` routes return `410 Gone` with the replacement endpoint.

Mount a volume to persist data: `-v tsflow_data:/app/data`

### Rolling back to the previous release

This release changes the SQLite schema. Before it does, startup writes a full copy of the database beside the live file. For `/app/data/tsflow.db` the copy is `/app/data/tsflow.db.pre-tailnet`. The log names that path. The copy needs about as much free disk as the database file, on top of the temporary space the migration uses while it rewrites tables.

The previous release cannot write the migrated file. Its inserts target the old primary keys, and it reads the poll cursor from `poll_state.id = 1`, which the new schema does not have. To run that release again:

1. Stop tsflow.
2. Replace the live database with the backup. For the path above, move `tsflow.db.pre-tailnet` to `tsflow.db`.
3. Delete `tsflow.db-wal` and `tsflow.db-shm` if they exist, so the restored file is not opened with a write-ahead log from the new process.
4. Start the previous release.

Rows saved after the upgrade are not in the backup. If startup cannot write the copy, it stops before changing the schema. Set `TSFLOW_SKIP_DB_BACKUP=1` to migrate without a rollback copy. The log says when that happens. Keep a backup of your own if you still want a way back.

## Development

### Setup

```bash
git clone https://github.com/rajsinghtech/tsflow.git
cd tsflow

# Install dependencies
cd frontend && npm install && cd ..
cd backend && go mod download && cd ..
```

### Development Mode

Run backend and frontend separately for hot reload:

```bash
# Terminal 1: Backend (no embedded frontend)
make dev-backend

# Terminal 2: Frontend with Vite dev server
make dev-frontend
```

Frontend runs on `http://localhost:5173` and proxies `/api` to backend on `:8080`.

### Production Build

```bash
make build
./backend/tsflow
```

This builds the SvelteKit frontend and embeds it in the Go binary.

## Deployment

### Docker Compose

```yaml
services:
  tsflow:
    image: ghcr.io/rajsinghtech/tsflow:latest
    ports:
      - "8080:8080"
    environment:
      - TAILSCALE_OAUTH_CLIENT_ID=${TAILSCALE_OAUTH_CLIENT_ID}
      - TAILSCALE_OAUTH_CLIENT_SECRET=${TAILSCALE_OAUTH_CLIENT_SECRET}
    volumes:
      - tsflow_data:/app/data
    restart: unless-stopped

volumes:
  tsflow_data:
```

### Kubernetes

```bash
cd k8s
# Requires envsubst (provided by gettext). Export the variables referenced by
# secret.yaml, then expand the template before applying the rendered manifests.
kubectl kustomize . | envsubst | kubectl apply -f -
```

Alternatively, replace the `${...}` placeholders in `k8s/secret.yaml` with
your credentials and run `kubectl apply -k k8s` directly. Do not apply the
template unchanged: Kubernetes does not expand shell-style environment
variables in manifests.

## Star History

[![Star History Chart](https://api.star-history.com/svg?repos=rajsinghtech/tsflow&type=Date)](https://star-history.com/#rajsinghtech/tsflow&Date)

## License

MIT

---

Built with ❤️ for the Tailscale community
