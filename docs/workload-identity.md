# Workload identity

tsflow has two credential paths that still default to long-lived secrets: reading flow logs from object storage, and calling the Tailscale API for each tailnet. This document is the config and auth design for replacing those secrets with workload identity. It is opt-in. A process that does not set the new variables keeps today's behavior.

tsnet node login is a third path and already has its own variables (`TS_CLIENT_ID`, `TS_ID_TOKEN`, `TS_AUDIENCE`). That path registers the embedded node. It does not authenticate API calls, and it does not read the bucket. The sections below say how the three paths differ.

## What the libraries actually do

### Tailscale API client

Module `tailscale.com/client/tailscale/v2` (v2.11.0) authenticates API calls in three ways:

- `APIKey` sends the key on each request.
- `OAuth` (and the older `OAuthConfig`) uses the OAuth client-credentials grant against `/api/v2/oauth/token`. `golang.org/x/oauth2/clientcredentials` caches the access token and refreshes it before expiry.
- `IdentityFederation` exchanges an OIDC ID token for a Tailscale API access token. The client posts `client_id` and `jwt` to `{BaseURL}/api/v2/oauth/token-exchange`. There is no client secret and no scope list on that request. Scopes and tag grants are fixed on the federated identity in the tailnet.

`IdentityFederation` takes a `ClientID` and an `IDTokenFunc`. The func returns a JWT. That token source keeps the JWT until its `exp` claim has already passed, then calls `IDTokenFunc` again. The exchanged API token is wrapped in `oauth2.ReuseTokenSource`, so later HTTP calls reuse it until shortly before `expires_in`.

tsflow uses the same exchange (`client_id` and `jwt` posted to `{BaseURL}/api/v2/oauth/token-exchange`) but owns the cache. A projected service account token is rotated on disk before it expires. Waiting until `exp` has passed can exchange a JWT the kubelet has already replaced, or one that is already expired. Each tailnet service therefore re-reads its ID token one minute before `exp` (sooner for a short-lived token) and refreshes the API token on the same lead. Calls in between reuse the cached API token. One tailnet does not exchange once per request or once per node.

The client library does not fetch the OIDC token from a cloud provider. The caller supplies the JWT. For platform discovery (audience, no JWT on disk) tsflow calls `tailscale.com/wif.ObtainProviderToken`, the same helper tsnet uses. In tailscale.com v1.104.0 that helper understands GitHub Actions, AWS (IMDS or ECS), and the GCP metadata server. It does not read a Kubernetes projected service account token, and it does not implement Azure. Those workloads pass the token in a file.

Upstream description: [Workload identity federation](https://tailscale.com/kb/1581/workload-identity-federation). The exchange endpoint is `https://api.tailscale.com/api/v2/oauth/token-exchange`.

### tsnet

`tsnet.Server` accepts `ClientID`, `IDToken`, and `Audience`. tsflow copies `TS_CLIENT_ID`, `TS_ID_TOKEN`, and `TS_AUDIENCE` onto the server in `internal/tsnetserve`. On `Up`, tsnet calls `HookResolveAuthKeyViaWIF` only when the program imports `tailscale.com/feature/identityfederation`. That import lives in `tsnetserve`, so the hook is linked. If the feature is disabled with `TS_DISABLE_FEATURE=identityfederation`, startup fails instead of ignoring `TS_CLIENT_ID` and falling through to a later login method.

The hook exchanges the JWT for an API token and then creates a one-time auth key for the tags in `TSFLOW_TAGS`. No `TS_AUTHKEY` is required. Set either `TS_ID_TOKEN` or `TS_AUDIENCE`, not both. `TS_AUDIENCE` asks the platform for a token. `TS_ID_TOKEN` is the token itself. OAuth client secret and `TS_AUTHKEY` login are unchanged.

One federated client per tailnet can cover both the API and the tsnet join. Give that identity `auth_keys` plus the node's tags, and the read scopes the poller uses. Point `TS_CLIENT_ID` at the same client id as that tailnet's API workload identity, and use the same token file or audience. A process still has one tsnet node, so the join stays on the process-wide `TS_*` variables. Each tailnet's API client stays its own, with its own cache. With several tailnets, the node joins as the `TS_*` identity and the other tailnets only use their API clients.

### Object storage

Flow objects are read with `github.com/aws/aws-sdk-go-v2/service/s3` and `github.com/aws/aws-sdk-go-v2/config`. There is no MinIO client. `NewObjectStoreSource` calls `config.LoadDefaultConfig`. If an access key or secret is present it installs `credentials.NewStaticCredentialsProvider` and the default chain is not used. The S3 client is selected when `TSFLOW_FLOW_BACKEND=s3`, or when the backend is unset and bucket, endpoint, access key, and secret are all set.

`LoadDefaultConfig` without a static provider uses the AWS SDK default credential chain, in this order:

1. Environment credentials (`AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN`).
2. Web identity (`AWS_ROLE_ARN`, `AWS_WEB_IDENTITY_TOKEN_FILE`, `AWS_ROLE_SESSION_NAME`). This is IRSA.
3. Shared config and credentials files, including a profile that assumes a role.
4. Container credentials (`AWS_CONTAINER_CREDENTIALS_RELATIVE_URI` or `AWS_CONTAINER_CREDENTIALS_FULL_URI`). This is ECS, and EKS Pod Identity.
5. EC2 instance metadata (the instance profile).

The SDK caches those credentials and refreshes them shortly before expiry. tsflow does not do a second cache on top of that chain.

GCS Application Default Credentials use `cloud.google.com/go/storage`, which is already required by the Tailscale module tsflow vendors. ADC covers the GCE or GKE metadata server (including GKE workload identity) and the file in `GOOGLE_APPLICATION_CREDENTIALS`. The storage client caches that token and refreshes it before expiry. The AWS SDK cannot present those credentials to the S3 API.

S3-compatible GCS is a different mode again. GCS exposes an XML API at `https://storage.googleapis.com` that takes HMAC keys. Those keys are static access-key credentials. They are not ADC, and they are not a Kubernetes service account token.

## Auth modes

### Tailscale API, per tailnet

| Mode | When it is used | Credentials |
| --- | --- | --- |
| `oauth` | `TAILSCALE_AUTH` unset and both OAuth client variables are set, or `TAILSCALE_AUTH=oauth`, or a file entry with OAuth fields | Client id and client secret. Scopes default to `all:read`. |
| `api_key` | `TAILSCALE_AUTH` unset and an API key is set without a full OAuth pair, or `TAILSCALE_AUTH=api_key`, or a file entry with an API key | API key. |
| `wif` | `TAILSCALE_AUTH=wif`, or a file entry with `auth.type: wif` | Federated client id, plus exactly one token source. |

`wif` is never inferred from a leftover variable. If a WIF variable is set and `TAILSCALE_AUTH` is not `wif`, startup fails. Existing OAuth-plus-API-key processes still prefer OAuth and log that, as they do today.

The token comes from one of:

- an ID token file (`TAILSCALE_WIF_ID_TOKEN_FILE`, or `id_token_file`), or
- an ID token value (`TAILSCALE_WIF_ID_TOKEN`, or `id_token_env` in the file), or
- an audience alone (`TAILSCALE_WIF_AUDIENCE`, or `audience`), which asks the platform for a token.

The client id and the audience are not secrets. The ID token is. The file format does not accept an inline token. A file entry may set `client_id` inline, or `client_id_env`, or `client_id_file`, and only one of those.

A file and an inline token together are a startup error. Audience may be set next to a file or an inline token. That is the Kubernetes shape: the projected token's audience is also recorded in config, and startup checks that the JWT `aud` claim includes it. Audience without a token is platform discovery. On GCP metadata, AWS STS `GetWebIdentityToken`, or GitHub Actions, omit the token and set the audience.

On Kubernetes, the usual source is a projected service account token whose audience is the federated identity's audience. tsflow reads that file again before the cached JWT expires. It does not ask the platform for another token.

OAuth scopes on a WIF entry are ignored. The federated identity already has its scopes.

### Object storage, process-wide

One bucket configuration is shared by every tailnet. Tailnets still override the prefix with `s3_prefix`. Per-tailnet buckets are out of scope.

| `TSFLOW_S3_AUTH` | Status | Reader |
| --- | --- | --- |
| unset or `static` | Implemented. This is the default. | Static access key and secret. Endpoint required. Path style defaults to true. |
| `aws_default` | Implemented. | AWS SDK default chain. Optional `TSFLOW_S3_ROLE_ARN` assumes a second role using those credentials, with the provider wrapped in `aws.NewCredentialsCache`. |
| `gcs_adc` | Implemented. | Native Cloud Storage client with Application Default Credentials. |

| `TSFLOW_FLOW_BACKEND` | Status |
| --- | --- |
| unset | `s3` when static endpoint, key, and secret are all set. `s3` when auth is `aws_default`. Otherwise `api`. |
| `api` | Tailscale API. Unchanged. |
| `s3` | S3 API, including S3-compatible endpoints and GCS HMAC interop. |
| `gcs` | Native Cloud Storage reader. Auth defaults to `gcs_adc`. |

`aws_default` does not install the static provider, even if `AWS_ACCESS_KEY_ID` is also present in the environment. The chain is supposed to see that variable. `TSFLOW_S3_ACCESS_KEY_ID`, `TSFLOW_S3_SECRET_ACCESS_KEY`, and the legacy `TAILSCALE_LOGS_S3_ACCESS_KEY` / `TAILSCALE_LOGS_S3_SECRET_KEY` variables are rejected in this mode so a static key is not silently ignored.

`aws_default` does not require an endpoint. An empty endpoint uses the SDK's regional S3 endpoint. A set endpoint is still checked as an absolute `http` or `https` URL, for a non-AWS S3 API that should use the default chain. Region comes from `TSFLOW_S3_REGION`, then `AWS_REGION`, then `AWS_DEFAULT_REGION`. It is required. The historical `garage` region default is not applied in this mode. Path style defaults to false when `TSFLOW_S3_PATH_STYLE` is unset, because AWS S3 uses virtual-hosted style. Set the variable to keep path style.

`TSFLOW_S3_ROLE_ARN` is only valid with `aws_default`. The pod or instance role from the chain calls STS `AssumeRole`. Session name is `tsflow`. The cache refreshes that credential. Do not point this at the role Tailscale itself assumes to write logs. That role trusts Tailscale's account and an external ID. tsflow does not send an external ID. The reader role should trust the workload identity, and the bucket policy should allow that role to list and get objects.

### Tailscale log streaming is not the reader

Tailscale can stream network flow logs into a bucket. On Tailscale's side that configuration is an IAM role ARN plus an external ID. Tailscale's servers assume the role and write objects. tsflow is a different principal that reads those objects. How the objects arrive and how tsflow authenticates are separate. `aws_default` configures the reader only.

GCS interop for a bucket that was filled that way is still `static` plus HMAC keys and `https://storage.googleapis.com`. The native reader is `TSFLOW_FLOW_BACKEND=gcs` with Application Default Credentials. It does not use the S3 endpoint or HMAC keys.

## Config surface

### Single-tailnet environment

API:

```
TAILSCALE_AUTH=wif
TAILSCALE_WIF_CLIENT_ID=<federated client id>
TAILSCALE_WIF_ID_TOKEN_FILE=/var/run/tsflow/token
```

Audience discovery instead of a file:

```
TAILSCALE_AUTH=wif
TAILSCALE_WIF_CLIENT_ID=<federated client id>
TAILSCALE_WIF_AUDIENCE=api.tailscale.com/<federated client id>
```

`TAILSCALE_AUTH` may also be `oauth` or `api_key`. Leave it unset to keep the current resolution.

Object storage:

```
TSFLOW_FLOW_BACKEND=s3
TSFLOW_S3_AUTH=aws_default
TSFLOW_S3_BUCKET=flow-logs
TSFLOW_S3_REGION=us-east-1
TSFLOW_S3_PREFIX=network/
```

Optional second hop: `TSFLOW_S3_ROLE_ARN=arn:aws:iam::123456789012:role/tsflow-reader`.

S3-compatible GCS, unchanged static mode:

```
TSFLOW_FLOW_BACKEND=s3
TSFLOW_S3_AUTH=static
TSFLOW_S3_ENDPOINT=https://storage.googleapis.com
TSFLOW_S3_ACCESS_KEY_ID=<hmac key>
TSFLOW_S3_SECRET_ACCESS_KEY=<hmac secret>
```

`static` may be omitted. Unset auth with an endpoint and keys is the same mode.

Native GCS:

```
TSFLOW_FLOW_BACKEND=gcs
TSFLOW_S3_BUCKET=flow-logs
TSFLOW_S3_PREFIX=network/
```

`TSFLOW_S3_AUTH=gcs_adc` is the same reader. Leave the endpoint and the static keys unset. On GKE the pod's service account is bound to a GCP service account, and the metadata server is what ADC uses. No key file is required there. Outside GCP, `GOOGLE_APPLICATION_CREDENTIALS` can point at a workload-identity credential configuration file.

### `TSFLOW_TAILNETS_FILE`

Flat `api_key_*` and `oauth_*` fields stay valid. An `auth` block is the opt-in for an explicit type. Do not set both.

```yaml
tailnets:
  - id: default
    tailnet: example.com
    s3_prefix: network/
    auth:
      type: wif
      client_id: <federated client id>
      id_token_file: /var/run/tsflow/default/token
  - id: lab
    tailnet: lab.example.com
    s3_prefix: lab/network/
    auth:
      type: oauth
      client_id_file: /secrets/lab/client-id
      client_secret_file: /secrets/lab/client-secret
      scopes:
        - all:read
  - id: west
    tailnet: west.example.com
    auth:
      type: api_key
      api_key_env: WEST_TAILSCALE_API_KEY
```

Audience form:

```yaml
auth:
  type: wif
  client_id_env: LAB_WIF_CLIENT_ID
  audience: api.tailscale.com/<federated client id>
```

Token from an environment variable (the variable's value is the JWT, not a file path):

```yaml
auth:
  type: wif
  client_id: <federated client id>
  id_token_env: LAB_WIF_ID_TOKEN
```

Object-store auth is not per entry. It stays on the process environment above. `s3_prefix` remains the per-tailnet override.

`TSFLOW_TAILNETS_FILE` still cannot be combined with `TAILSCALE_TAILNET`, `TAILSCALE_API_KEY`, OAuth client variables, `TAILSCALE_AUTH`, or the `TAILSCALE_WIF_*` variables.

## Token refresh and caching

Each tailnet gets its own `tailscale.Client` and its own `IdentityFederation` token source. Sources are not shared across tailnets, and a refresh for one tailnet does not exchange a token for another.

Within one tailnet the cache is two layers, both on that tailnet's token source:

1. The ID token is kept until one minute before `exp` (or one fifth of its life, when that is sooner). A file source reads the file again at that point, so a kubelet can rotate the projected token on disk without a restart. An environment-variable token is the value captured at startup. Rotating it means restarting the process.
2. The Tailscale API access token is kept until the same lead time before `expires_in`. Parallel chunk requests and device-list calls in that tailnet reuse it. The exchange uses the ID token that was refreshed first.

A tailnet of about 20k nodes does not exchange once per node. Device refresh is one list call. Flow import is a sequence of time-range calls. All of them share the tailnet's cached API token. Audience discovery is the expensive source: `ObtainProviderToken` may spend a few seconds probing metadata, and an AWS web identity token lives about five minutes. That still happens once per tailnet per ID-token lifetime, then the API token cache covers the poll.

AWS credentials are cached inside the SDK provider. The optional assume-role provider is wrapped in `aws.NewCredentialsCache`, so a poll that lists and downloads many objects does not call STS per object. Several tailnets share that one process-wide S3 client configuration. They do not assume the role separately.

Startup checks that a configured token file exists and is non-empty. It does not exchange a token and it does not call STS. A bad JWT or a missing cloud credential fails the first API or S3 call that needs it. There is no silent fallback to an API key, an OAuth secret, or static S3 keys.

## Per-tailnet isolation

`TailnetSpec` carries the resolved auth mode and WIF material for that entry. `NewTailscaleService` builds the client from that spec alone. OAuth client-credentials caches and WIF caches live inside the HTTP client for that service. The registry still runs one poller goroutine per tailnet, and a slow token exchange in one tailnet does not block another's poll loop beyond the usual startup sequencing.

Object storage is the exception: one credential, one bucket, many prefixes. That matches the current poller, which already shares `ObjectStoreConfig` and overrides `Prefix`.

tsnet WIF is the one embedded node for the process. Use the same federated client as that tailnet's API identity when one client should both join and read. Separate client ids still work if the scopes must differ. tsflow does not copy API WIF fields onto the node automatically.

## Validation errors

Startup fails in `Config.Validate` before the pollers start.

Tailnet API:

- `TAILSCALE_AUTH` must be `oauth`, `api_key`, or `wif`.
- `wif` requires `TAILSCALE_WIF_CLIENT_ID`, and a token file, an inline token, or an audience. A file and an inline token together are rejected. Audience may accompany one token, and the token's `aud` claim must include it.
- `wif` cannot be combined with an API key or an OAuth client.
- Explicit `oauth` requires both client id and secret, and cannot be combined with an API key or WIF fields.
- Explicit `api_key` requires `TAILSCALE_API_KEY`, and cannot be combined with OAuth or WIF fields.
- WIF fields with `TAILSCALE_AUTH` unset: `workload identity federation fields require TAILSCALE_AUTH=wif`.
- A token file must exist and be non-empty. `id_token_env` must be set and non-empty.
- File entries: unknown fields fail, including an inline `id_token`. `auth` plus flat `api_key_*` or `oauth_*` fields fails. Missing client id, missing token source, or two token sources fail with the tailnet id in the message.

Object storage:

- `gcs` and `gcs_adc` require a bucket, reject an S3 endpoint, and reject the static key variables named above. Missing Application Default Credentials fail when the reader is constructed, not by falling back to static keys.
- `gcs_adc` with `TSFLOW_FLOW_BACKEND=s3` or `api` is rejected. Empty auth with backend `gcs`, or `gcs_adc` with an empty backend, selects the native reader.
- Any other backend or auth value is rejected by name.
- `aws_default` requires `TSFLOW_FLOW_BACKEND=s3` when the backend is set to `api`, requires a bucket and a region, and rejects the static key variables named above.
- `aws_default` with an empty backend selects `s3`.
- `TSFLOW_S3_ROLE_ARN` requires `aws_default`.
- `static` S3 still requires bucket, endpoint, access key, and secret. Endpoint scheme and lookback rules are unchanged.

`NewObjectStoreSource` repeats the GCS endpoint rejection. A caller that skips `Validate` still cannot treat a native GCS bucket as an S3 endpoint, and it cannot fall through to static keys.

## Not in this change

- Per-tailnet object-store credentials or buckets.
- An external ID on `AssumeRole`.
- Azure metadata discovery for audience mode.

## Kubernetes examples

Generic sketches. They are not tied to a cluster or an account.

### IRSA for the bucket

The service account is annotated with the reader role. The webhook injects `AWS_ROLE_ARN` and `AWS_WEB_IDENTITY_TOKEN_FILE`. No access keys are mounted.

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: tsflow
  annotations:
    eks.amazonaws.com/role-arn: arn:aws:iam::123456789012:role/tsflow-reader
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: tsflow
spec:
  template:
    spec:
      serviceAccountName: tsflow
      containers:
        - name: tsflow
          image: ghcr.io/rajsinghtech/tsflow:latest
          env:
            - name: TSFLOW_FLOW_BACKEND
              value: s3
            - name: TSFLOW_S3_AUTH
              value: aws_default
            - name: TSFLOW_S3_BUCKET
              value: flow-logs
            - name: TSFLOW_S3_REGION
              value: us-east-1
            - name: TSFLOW_S3_PREFIX
              value: network/
            - name: TAILSCALE_OAUTH_CLIENT_ID
              valueFrom:
                secretKeyRef:
                  name: tsflow
                  key: oauth-client-id
            - name: TAILSCALE_OAUTH_CLIENT_SECRET
              valueFrom:
                secretKeyRef:
                  name: tsflow
                  key: oauth-client-secret
```

EKS Pod Identity and an EC2 instance profile use the same `TSFLOW_S3_AUTH=aws_default` block. They differ only in how the platform presents the default chain. Add `TSFLOW_S3_ROLE_ARN` only when the workload role must assume a second role to read the bucket.

The writer role used by Tailscale log streaming is not `tsflow-reader`.

### GKE workload identity

Annotating the Kubernetes service account with `iam.gke.io/gcp-service-account` lets the pod use the GCP metadata server. That is the credential the native GCS reader uses.

The same metadata server can mint an OIDC token for Tailscale. That part is implemented, through audience discovery:

```yaml
env:
  - name: TAILSCALE_AUTH
    value: wif
  - name: TAILSCALE_WIF_CLIENT_ID
    value: <federated client id>
  - name: TAILSCALE_WIF_AUDIENCE
    value: api.tailscale.com/<federated client id>
```

The federated identity in Tailscale must trust the GCP issuer and the service account subject. The node pool needs workload identity enabled so the metadata server will sign tokens for that audience.

Reading that same bucket with HMAC keys remains the static S3 interop configuration. Native reads use `TSFLOW_FLOW_BACKEND=gcs` and do not set an endpoint or HMAC keys.

### Projected service account token for Tailscale WIF

Use this when the cluster's OIDC issuer is the identity Tailscale trusts (typical on Kubernetes, including EKS). The audience on the projected token is the federated identity's audience. Set that same audience in tsflow so startup can reject a token minted for a different audience. The file is what gets exchanged.

```yaml
spec:
  serviceAccountName: tsflow
  containers:
    - name: tsflow
      env:
        - name: TAILSCALE_AUTH
          value: wif
        - name: TAILSCALE_WIF_CLIENT_ID
          value: <federated client id>
        - name: TAILSCALE_WIF_AUDIENCE
          value: api.tailscale.com/<federated client id>
        - name: TAILSCALE_WIF_ID_TOKEN_FILE
          value: /var/run/tsflow/token
      volumeMounts:
        - name: tailscale-token
          mountPath: /var/run/tsflow
          readOnly: true
  volumes:
    - name: tailscale-token
      projected:
        sources:
          - serviceAccountToken:
              path: token
              expirationSeconds: 3600
              audience: api.tailscale.com/<federated client id>
```

`TS_CLIENT_ID`, `TS_ID_TOKEN`, and `TS_AUDIENCE` stay the tsnet node settings. The API client does not read them. `TAILSCALE_WIF_AUDIENCE` is optional next to the file. When it is set, it must match the token `aud`.

A multi-tailnet pod projects one token file per tailnet (different audiences or different mounted paths) and points each `id_token_file` at its own file. Each tailnet's client caches only its own exchange.
