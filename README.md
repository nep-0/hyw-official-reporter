# Heyinwu official QQ reporter

Browser-free Go service forwarding Heyinwu admin **broadcast records** (播报记录), including text and image records, through an official QQ group bot. No dependencies beyond Go 1.27.4.

## Configuration

For a new installation, copy `.env.example` to `.env` and fill in credentials. Keep an existing `.env`; the template contains no credentials or shop-specific defaults.

Existing `.env` is read automatically. Process environment overrides file values. Supported dotenv syntax: blank lines, full-line `#` comments, `KEY=value`, and single/double-quoted values; no shell expansion or inline comments.

```dotenv
HYW_ADMIN_USERNAME="your-admin-account"
HYW_ADMIN_PASSWORD="your-admin-password"
# Optional for shop-specific accounts; defaults to the login response's shop_id.
HYW_SHOP_ID=45
QQ_BOT_APPID="your-app-id"
QQ_BOT_SECRET="your-app-secret"
QQ_BOT_GROUP_OPENID="your-bot-specific-group-openid"
```

`QQ_BOT_GROUP_OPENID` is the opaque group ID from this bot's QQ event, **not** the numeric QQ group number in Heyinwu records. Enable the bot's group messaging permissions and obtain its group ID through your existing QQ bot integration. Active message permissions and platform quotas apply; the service cannot bypass them.

```sh
go run . -mode shops
go run . -mode fetch
go run . -mode watch -interval 30s -state data/state.json
```

Default `fetch` prints the newest page (up to 50 full records) to stdout. It never sends QQ messages or changes local delivery state. Output may contain private broadcast content. `shops` lists shop IDs/names. Both authenticate using `.env`.

`watch` first snapshots all retained records without sending history. Later polls paginate through retained records and deliver unseen records oldest-first. Both successful and failed original Heyinwu broadcasts are forwarded: upstream `status` describes the original delivery, not this reporter. State is stored locally and bound to the selected shop and QQ destination. No checkbox, note, or delivery flag is written to Heyinwu.

Keep the state file across restarts. Deleting it establishes a new baseline and skips currently retained history. Run one process per state file. A `.lock` file prevents concurrent local processes; after a crash, verify the old process is stopped before removing its stale lock. Errors stop the process with a nonzero exit code; use a supervisor to restart it. Ctrl+C or SIGTERM shuts down and releases the lock.

Delivery is **at least once**, not exactly once: a crash after QQ accepts a message but before checkpointing can duplicate it. Image records send text followed by images; a partial failure may repeat already-sent parts. API retention is 30 days, so longer outages can lose expired records. Offset pagination is not an atomic snapshot; newly inserted records are picked up on subsequent polls.

## Container image and GitHub Actions

`.github/workflows/container-image.yml` follows `rmj-card-go`: pushes to `main` and `v*.*.*` tags build and publish an OCI image to GHCR; pull requests build without publishing. Tags include the branch/tag, commit SHA, and `latest` on the default branch. Authentication uses `GITHUB_TOKEN` with package-write permission; no Heyinwu or QQ secrets are needed in CI.

The multi-stage Dockerfile uses the same Go/Alpine build and `su-exec` startup convention as the reference. The process runs as `app` and keeps state in `/app/data`. `.dockerignore` excludes `.env` files and local state from the build context. The default image command remains a read-only fetch; pass `-mode watch` explicitly for forwarding.

```sh
docker build -t hyw-official-reporter .
docker run --rm --env-file .env hyw-official-reporter
docker run --rm --env-file .env -v hyw-reporter-data:/app/data hyw-official-reporter hyw-official-reporter -mode watch
```

Preserve the named volume across restarts to retain checkpoints. For published images, replace the local image name with `ghcr.io/<owner>/<repository>:latest`.

## Safety and implementation

The admin HTTP client allowlists only `login`, `shop_list`, `tell_log_list`, and `tell_log_detail`. All use JSON POST because that is the site's protocol. No browser automation, logout, moderation, configuration changes, note updates, or `done` updates. Login creates a session; production authentication may have server-side audit/session effects. Credentials/tokens are never deliberately logged. Redirects are rejected to avoid forwarding credentials to another endpoint.

The existing `.env` is left unchanged. QQ delivery has been exercised using an isolated HTTP transport, not a real group. To start live forwarding, supply QQ credentials and the bot-specific destination, then explicitly use `-mode watch`.

## Research sources

- [Heyinwu admin page](https://heyinwu.top/admin.html) loads the first-party [admin JavaScript](https://heyinwu.top/res/js/main.js?v=20261117d). `use.login` posts `{id, pass, token:""}` to `https://wechat.heyinwu.top/api/web/login`; successful replies contain `data.token` and `data.shop_id`.
- The same script's `http.api_post` injects `token` into the JSON body; application code `455` means the session expired. The client reauthenticates once and retries the read.
- `tell_func.load` uses `tell_log_list` with `{shop_id,page,size,kw:""}`, returning `data.list` and `data.total`. `tell_func.detail` uses `tell_log_detail` with `{shop_id,rid}`, returning `msg` and `imgs`. Records are retained for 30 days. `tell_log_note` writes notes/checkmarks and is deliberately excluded.
- Live read-only verification authenticated the supplied account, read its shop list, fetched two broadcast summaries, and fetched one matching detail. Times are Unix milliseconds; records have stable string RIDs. No admin write endpoint was invoked.
- QQ matches `../rmj-card-go/qqbot.go`'s proactive `PostGroupMessage` pattern. Wire protocol comes from Tencent's official [botgo token implementation](https://github.com/tencent-connect/botgo/blob/v0.2.1/token/token_source.go), [group routes](https://github.com/tencent-connect/botgo/blob/v0.2.1/openapi/v1/resource.go), [authorization](https://github.com/tencent-connect/botgo/blob/v0.2.1/openapi/v1/openapi.go), and [message DTOs](https://github.com/tencent-connect/botgo/blob/v0.2.1/dto/message_create.go). Images upload with `srv_send_msg:false`, then send type `7` with the returned `file_info`.

## Verification

`go test ./...` passed: baseline suppression, pagination across 55 new records, oldest-first delivery, failed-send retry, restart deduplication, destination mismatch rejection, and the QQ token/text/image HTTP flow.

`go run . -mode fetch` succeeded against production using the existing `.env`: 95 available records, 50 full records decoded, including 12 with image URLs. No real QQ message was sent.

Container/CI checks: `actionlint .github/workflows/container-image.yml`, `sh -n docker-entrypoint.sh`, and a Linux amd64 static cross-build with the Dockerfile's build flags passed. CLI help was exercised without contacting production. A local Docker image build was not run because Docker is unavailable; the GitHub-hosted workflow still needs its first run.
