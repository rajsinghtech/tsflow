# Configuration audit logs

Tailscale keeps a configuration audit log: who signed in, approved a device,
changed tags or routes, edited the policy file, made keys, or changed
settings. Tailscale keeps 90 days of it. tsflow can keep a longer, searchable
copy next to your flow data, show it in an **Audit** tab, and answer "when did
this person last log in?".

It is off by default. Nothing is read or stored until you turn it on.

## Turn it on

```sh
TSFLOW_AUDIT_ENABLED=true
TSFLOW_AUDIT_VIEWERS=you@example.com,group:security   # who may read it
TSFLOW_AUDIT_BACKEND=gcs                              # gcs, s3 or api
TSFLOW_AUDIT_BUCKET=example-audit-logs
TSFLOW_AUDIT_PREFIX=configuration/
```

All settings are listed in [environment.md](environment.md#configuration-audit-logs).

## Where the logs come from

**Log streaming to a bucket (recommended).** In the Tailscale admin console,
stream configuration logs to S3 or GCS. Tailscale uploads a gzip file about
every five minutes under `<prefix>YYYY/MM/DD/`. tsflow lists the newest day
folders, reads files it hasn't seen, and remembers each file so a restart
never reads it twice. Bucket access works the same way as the flow log
backends (`TSFLOW_AUDIT_AUTH` takes the same values as `TSFLOW_S3_AUTH`; GCS
uses application default credentials).

**The Tailscale API.** `TSFLOW_AUDIT_BACKEND=api` reads
`/api/v2/tailnet/{tailnet}/logging/configuration` a day at a time. The API
credentials need the `logs:configuration:read` scope. The API only keeps 90
days, so the first poll can backfill at most 90 days.

## Several tailnets

With a tailnets file, give each tailnet its own `audit:` block. The
`TSFLOW_AUDIT_*` variables fill in anything a block leaves out. A tailnet
without a block is not read (unless it's the only tailnet), so two tailnets
never share one prefix by accident.

```yaml
tailnets:
  - id: prod
    tailnet: example.com
    api_key_env: PROD_TS_API_KEY
    audit:
      backend: gcs
      bucket: example-audit-logs
      prefix: prod/configuration/
  - id: lab
    tailnet: lab.example.com
    api_key_env: LAB_TS_API_KEY
    audit:
      backend: api
```

Block fields: `backend`, `bucket`, `prefix`, `region`, `endpoint`, `s3_auth`,
`role_arn`, `web_identity_token_file`.

## Who can see it

Audit data shows who did what, so it has its own permission:

- `TSFLOW_AUDIT_VIEWERS` lists logins and groups (`group:` prefix optional).
- With access control on, only those viewers get the Audit tab, the
  `/api/audit` routes and the MCP audit tools. Everyone else gets 403 and
  doesn't see the tab or the tools. An empty list allows nobody.
- With access control off, anyone who can reach tsflow can read it, the
  same as flow data.
- A viewer's tailnet allowlist still applies.

## Storage

Events go in their own SQLite file (`audit.db` next to the flow database by
default), so audit writes never wait behind large flow writes. Each record is
stored once: Tailscale records have no id, so tsflow keys each one by a hash
of the whole record. Retention defaults to 400 days
(`TSFLOW_AUDIT_RETENTION`), and cleanup runs hourly.

If the audit source fails to start, tsflow logs it and keeps serving flows.

## Using it

- **Audit tab, Events view**: search; category chips (logins, policy file,
  devices, users and groups, keys and credentials, tailnet settings, DNS,
  sharing and invites, integrations, Tailnet Lock, billing, PAM, failed
  requests) with counts; chips for failed changes and ephemeral devices; an
  activity chart (click a bar to zoom into that hour or day); counts by
  category, event, actor, kind of actor (person, OAuth client, Tailscale
  service, SCIM, ...), what changed, target type and where the change was
  made (admin console, API, device, identity provider, ...); and an event
  drawer with old and new values, actor id and tags, the operation id, and
  whether Tailscale's rate limiter delayed the record. Policy file edits show
  as a line diff, and the drawer lists the other events from the same
  operation (for example, the five records Tailscale writes when a device is
  added).
- **Audit tab, Overview**: every policy file edit with lines added and
  removed; the current state of each tailnet setting that changed (device
  approval, MagicDNS, key duration, log streaming, ...); a lifecycle table for
  keys (created, revoked or expired, by whom, tags, scopes, reusable,
  ephemeral, pre-approved, expiry); and the most active people and targets.
- **Device history** (`/audit?targetId=<node id>`): a summary card with when
  the device was added, approved, last logged in and out, key expiry (or
  expiry disabled), tags, subnet and auto-approved routes, Tailnet Lock
  signing, and removal.
- **Export**: CSV (one row per event) or NDJSON (the original Tailscale
  records) for the current filters, up to 100,000 events.
- **User page** (`/audit/user/<login>`): last device login, last admin
  console login, last web interface login, the users API "last seen", the
  user's devices, and a timeline of everything they did or had done to them.
- **Cross-links**: device events link to the traffic view for that device an
  hour either side of the event. The device panel on the traffic page links
  back to that device's audit history.

"Last seen" from the users API is not a login. It moves whenever one of the
user's devices is online. A device login is recorded when a device signs in,
including re-auth after key expiry.

### Labels and categories

tsflow labels every event in Tailscale's API spec (140 today) and puts it in
a category. An event Tailscale adds later still shows up, with a label built
from its name and a category from its target type.

### Keys

Tailscale logs auth keys, OAuth clients, federated identities and API access
tokens all as target type `API_KEY`. tsflow uses the record's `keyType` when
present, and otherwise works the kind out from what the key can do (scopes
mean an OAuth client, device capabilities mean an auth key, an issuer means a
federated identity). A key created before the stored history starts shows as
"earlier".

### What Tailscale does not log

These never reach the audit log, so tsflow cannot show them: changes made by
Tailscale support, failed logins, the suspension of a suspended user's devices
and keys, and the deletion of a deleted user's keys. Billing records carry no
old or new values. Tailscale keeps audit logs for 90 days; tsflow keeps its
copy for `TSFLOW_AUDIT_RETENTION`.

## API

| Route | Returns |
| --- | --- |
| `GET /api/audit/status` | Whether audit is on, whether you may read it, retention, and each tailnet's source status |
| `GET /api/audit/events` | Events, newest first. Filters: `since` (`30d`) or `start`/`end`, `actor` (`~text` matches part of a name, or an exact actor id or type), `target`, `targetId`, `user`, `event` (`NODE.*` matches a prefix), `category`, `action`, `targetType`, `actorType`, `origin`, `failed` (failed changes and refused requests), `ephemeral`, `q`. Page with `cursor` from `nextCursor`; `limit` up to 1000 |
| `GET /api/audit/events/:id` | One event and the others from the same operation |
| `GET /api/audit/groups/:groupId` | Every event from one operation |
| `GET /api/audit/facets` | Counts by category, event, actor, actor type, target, target type and origin, plus failed and ephemeral totals, for the same filters |
| `GET /api/audit/insights` | Activity per hour or day (with failed and per-category counts), policy file edits, key lifecycles and tailnet settings. `tzOffset` (seconds east of UTC) lines day bars up with local midnight |
| `GET /api/audit/export` | The matching events as `format=csv` or `format=ndjson` (original records), up to 100,000 |
| `GET /api/audit/catalog` | Every known event with its label and category |
| `GET /api/audit/users/:login` | The user summary, live users and devices API data, and the timeline. `live=false` skips the API calls |
| `GET /api/audit/nodes/:nodeId` | One device's summary and audit history |

List filters may repeat (`event=NODE.LOGIN&event=ADMIN_CONSOLE.LOGIN`).

## MCP tools

With MCP on, viewers allowed to read audit data also get:

| Tool | Answers |
| --- | --- |
| `audit_search` | Who changed what and when, with the same filters as the API (including category, actor type and ephemeral) |
| `audit_overview` | Totals by category, failed count, the busiest hours or days, policy file edits, key lifecycles and changed settings for a time range |
| `device_audit` | One device's summary (added, approved, logins, key expiry, tags, routes, removal) and its recent events |
| `user_last_login` | A user's last device, console and web logins, the users API last seen, their devices, and what each time means |
