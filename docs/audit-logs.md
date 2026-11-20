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

- **Audit tab**: search, quick filters (logins, policy file, devices, users,
  keys, failed), counts by event, actor and target type, and an event drawer
  with old and new values. Policy file edits show as a line diff.
- **User page** (`/audit/user/<login>`): last device login, last admin
  console login, last web interface login, the users API "last seen", the
  user's devices, and a timeline of everything they did or had done to them.
- **Cross-links**: device events link to the traffic view for that device an
  hour either side of the event. The device panel on the traffic page links
  back to that device's audit history.

"Last seen" from the users API is not a login. It moves whenever one of the
user's devices is online. A device login is recorded when a device signs in,
including re-auth after key expiry.

## API

| Route | Returns |
| --- | --- |
| `GET /api/audit/status` | Whether audit is on, whether you may read it, retention, and each tailnet's source status |
| `GET /api/audit/events` | Events, newest first. Filters: `since` (`30d`) or `start`/`end`, `actor` (`~text` matches part of a name), `target`, `targetId`, `user`, `event` (`NODE.*` matches a prefix), `action`, `targetType`, `origin`, `failed`, `q`. Page with `cursor` from `nextCursor`; `limit` up to 1000 |
| `GET /api/audit/events/:id` | One event and the others from the same operation |
| `GET /api/audit/groups/:groupId` | Every event from one operation |
| `GET /api/audit/facets` | Counts by event, actor, target type and origin for the same filters |
| `GET /api/audit/users/:login` | The user summary, live users and devices API data, and the timeline. `live=false` skips the API calls |
| `GET /api/audit/nodes/:nodeId` | One device's audit history |

List filters may repeat (`event=NODE.LOGIN&event=ADMIN_CONSOLE.LOGIN`).

## MCP tools

With MCP on, viewers allowed to read audit data also get:

| Tool | Answers |
| --- | --- |
| `audit_search` | Who changed what and when, with the same filters as the API |
| `user_last_login` | A user's last device, console and web logins, the users API last seen, their devices, and what each time means |
