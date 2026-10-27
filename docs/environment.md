# Environment variables

Every variable tsflow reads, with its default. `backend/internal/config/envdoc_test.go` scans the Go source for environment reads and fails when one is missing here, so this list stays in sync with the code.

Nothing here is required beyond credentials: an API key or an OAuth client for the API backend, or bucket settings for S3 and GCS. Everything new in 1.7 is off or keeps the old behavior until you set it.

Any variable read through the config helpers also accepts a `VITE_` prefix, for example `VITE_TAILSCALE_API_KEY`. The unprefixed name wins when both are set. This is a leftover from the old frontend build and still works.

## Tailscale API

| Variable | Default | Notes |
|----------|---------|-------|
| `TAILSCALE_API_KEY` | - | API key. Use this or an OAuth client. |
| `TAILSCALE_OAUTH_CLIENT_ID` | - | OAuth client id. Needs the secret too. |
| `TAILSCALE_OAUTH_CLIENT_SECRET` | - | OAuth client secret. Wins over an API key when both are set. |
| `TAILSCALE_OAUTH_SCOPES` | `all:read` | Comma-separated. |
| `TAILSCALE_AUTH` | - | `oauth`, `api_key`, or `wif`. Unset picks from the credentials that are set, as before. |
| `TAILSCALE_WIF_CLIENT_ID` | - | Federated client id for API calls. Needs `TAILSCALE_AUTH=wif`. |
| `TAILSCALE_WIF_ID_TOKEN` | - | OIDC token. Set one token source only. |
| `TAILSCALE_WIF_ID_TOKEN_FILE` | - | File with the OIDC token. |
| `TAILSCALE_WIF_AUDIENCE` | - | Audience for asking the platform for a token. |
| `TAILSCALE_TAILNET` | `-` | Tailnet name. `-` means the tailnet the credential belongs to. |
| `TAILSCALE_API_URL` | `https://api.tailscale.com` | |
| `TSFLOW_TAILNETS_FILE` | - | YAML or JSON list of tailnets. Unset means one tailnet, id `default`, from the variables above. Don't set both. An entry's optional `display_name` labels it in the header switcher. See [Several tailnets](../README.md#several-tailnets). |

## Server

| Variable | Default | Notes |
|----------|---------|-------|
| `PORT` | `8080` | |
| `ENVIRONMENT` | `development` | `development` or `production`. |
| `ALLOWED_CORS_ORIGINS` | - | Comma-separated. Unset, production allows none and development allows loopback origins only. |
| `TSFLOW_TRUSTED_PROXIES` | - | Proxy IPs or CIDRs allowed to set the client address with `X-Forwarded-For`. That address keys the rate limit (100 requests a minute) and shows in request logs. Unset means the TCP peer. |
| `TSFLOW_MCP_ENABLED` | `false` | Serve a read-only MCP endpoint at `/mcp`. |
| `TSFLOW_METRICS_ADDR` | - | Serve Prometheus metrics at `/metrics` on this address, for example `:9090`. Its own plain listener, never behind access control. Set it to `PORT` to share the UI listener. See [Metrics](metrics.md). |
| `TSFLOW_METRICS_TRAFFIC` | `false` | Add traffic metrics: byte, packet and flow counters by traffic type and protocol, and the last 15 minutes by tag, user and port, top 20 each. Needs `TSFLOW_METRICS_ADDR`. See [Traffic metrics](metrics.md#traffic-metrics). |
| `TSFLOW_METRICS_TOP_TALKERS` | `0` | List this many top devices by bytes over the last 15 minutes, up to 100. `0` lists none. Needs `TSFLOW_METRICS_ADDR`. |
| `TSFLOW_LOG_LEVEL` | - | `debug` logs every denied request instead of one per peer and reason a minute. |

## tsnet serve mode

| Variable | Default | Notes |
|----------|---------|-------|
| `TSFLOW_SERVE` | `false` | Run as a node on the tailnet. Needs OAuth or workload identity, not an API key. |
| `TSFLOW_HOSTNAME` | `tsflow` | |
| `TSFLOW_TAGS` | - | Comma-separated, for example `tag:tsflow`. |
| `TSFLOW_FUNNEL` | `false` | Refused with more than one tailnet. |
| `TSFLOW_STATE_DIR` | `./data/tsnet-state` | |
| `TSFLOW_HEALTH_PORT` | - | Plain port serving only `/health`, for probes. Only with `TSFLOW_SERVE=true`. |
| `TS_CLIENT_ID` | - | Federated client id for the tsnet node. |
| `TS_ID_TOKEN` | - | Set exactly one of these three. |
| `TS_ID_TOKEN_FILE` | - | Read once at startup. |
| `TS_AUDIENCE` | - | |

## Access control

Off unless you set it. See [Internal access control](../README.md#internal-access-control).

| Variable | Default | Notes |
|----------|---------|-------|
| `TSFLOW_ACCESS_MODE` | - | `tsnet` or `header`. |
| `TSFLOW_ACCESS_CAPABILITY` | - | App capability name, for example `example.com/cap/tsflow`. |
| `TSFLOW_ACCESS_GRANTS` | `required` | `identity` lets any known identity see every tailnet. |
| `TSFLOW_ACCESS_TRUSTED_PROXIES` | - | Required in header mode. Identity headers are read only from these CIDRs. |
| `TSFLOW_ACCESS_USER_HEADER` | `Tailscale-User-Login` | |
| `TSFLOW_ACCESS_NAME_HEADER` | `Tailscale-User-Name` | |
| `TSFLOW_ACCESS_CAPABILITY_HEADER` | `Tailscale-App-Capabilities` | |
| `TSFLOW_ACCESS_GROUPS_HEADER` | - | Needs a group grant map. |
| `TSFLOW_ACCESS_GROUP_GRANTS` | - | JSON map of group or login to grant. |
| `TSFLOW_ACCESS_GROUP_GRANTS_FILE` | - | Same map from a file. Don't set both. |
| `TSFLOW_ACCESS_LOCAL_WHOIS` | `auto` | `auto`, `off`, or `require`. Header mode only. |
| `TSFLOW_ACCESS_TAILSCALED_SOCKET` | tailscaled default | Setting it makes the socket required. |
| `TSFLOW_ACCESS_AUTOSCOPE` | `off` | `user` or `groups` preselects the viewer's devices. |

## Storage and polling

| Variable | Default | Notes |
|----------|---------|-------|
| `TSFLOW_DB_PATH` | `./data/tsflow.db` | |
| `TSFLOW_SKIP_DB_BACKUP` | - | `1` skips the copy made before the 1.7 migration. |
| `TSFLOW_POLL_INTERVAL` | `5m` | |
| `TSFLOW_INITIAL_BACKFILL` | `6h` | |
| `TSFLOW_POLL_DELAY` | `2m` | API backend only. Polls stop this far behind now. `0` polls up to now. |
| `TSFLOW_GRAPH_CACHE_MB` | `256` | Cache of closed hours for the graph. `0` turns it off. |
| `TSFLOW_RETENTION` | `720h` (API), off (S3, GCS) | `0` turns cleanup off. |

## Flow backend and buckets

| Variable | Default | Notes |
|----------|---------|-------|
| `TSFLOW_FLOW_BACKEND` | `api` | `api`, `s3`, or `gcs`. Unset with an S3 endpoint and keys set means `s3`, as before. |
| `TSFLOW_S3_AUTH` | `static` | `static`, `aws_default`, or `gcs_adc`. |
| `TSFLOW_S3_BUCKET` | `tailscale-logs` | |
| `TSFLOW_S3_PREFIX` | `network/` | |
| `TSFLOW_S3_ENDPOINT` | - | S3-compatible endpoint URL. |
| `TSFLOW_S3_REGION` | `garage` (static) | `aws_default` uses the SDK region. |
| `TSFLOW_S3_ACCESS_KEY_ID` | - | |
| `TSFLOW_S3_SECRET_ACCESS_KEY` | - | |
| `TSFLOW_S3_PATH_STYLE` | `true`, `false` with `aws_default` | Path-style bucket addressing. |
| `TSFLOW_S3_LOOKBACK` | `15m` | Listing overlap for late objects. |
| `TSFLOW_S3_MAX_OBJECTS_PER_POLL` | `500` | |
| `TSFLOW_S3_ROLE_ARN` | - | Role to assume. Needs `TSFLOW_S3_AUTH=aws_default`. |
| `TSFLOW_S3_WEB_IDENTITY_TOKEN_FILE` | - | OIDC token file used to assume that role. |

### Older names that still work

These are read when the `TSFLOW_S3_*` name is unset, in this order.

| Setting | Also read from |
|---------|----------------|
| bucket | `TAILSCALE_LOGS_S3_BUCKET` |
| prefix | `TAILSCALE_LOGS_S3_PREFIX` |
| endpoint | `TAILSCALE_LOGS_S3_ENDPOINT`, `AWS_ENDPOINT_URL`, `endpoint` |
| region | `AWS_REGION`, `AWS_DEFAULT_REGION`, `region` |
| access key | `TAILSCALE_LOGS_S3_ACCESS_KEY`, `AWS_ACCESS_KEY_ID` |
| secret key | `TAILSCALE_LOGS_S3_SECRET_KEY`, `AWS_SECRET_ACCESS_KEY` |

### Read by the cloud SDKs

tsflow doesn't read these itself, but the SDKs do:

- `aws_default` uses the normal AWS chain: `AWS_ROLE_ARN`, `AWS_WEB_IDENTITY_TOKEN_FILE`, `AWS_PROFILE`, instance and pod identity.
- `gcs` and `gcs_adc` use Application Default Credentials, so `GOOGLE_APPLICATION_CREDENTIALS` works.
- `STORAGE_EMULATOR_HOST` points GCS at an emulator and skips credentials. For tests.
