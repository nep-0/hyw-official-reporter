# Heyinwu official QQ reporter

Browser-free Go service forwarding Heyinwu admin **broadcast records** (播报记录), including text and image records, through an official QQ group bot. Requires Go 1.27.4 and uses Tencent `botgo v0.2.1`, matching `rmj-card-go`. Dependencies are managed with `go get` and `go mod tidy`.

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

`fetch` prints the newest page (up to 20 full records) to stdout. Watch-mode pagination also fetches 20 records per request. Preview mode never sends QQ messages or changes local delivery state. Output may contain private broadcast content. `shops` lists shop IDs/names. Both authenticate using `.env`.

`watch` first snapshots all retained records without sending history. Later polls paginate through retained records and deliver unseen records oldest-first. Both successful and failed original Heyinwu broadcasts are forwarded: upstream `status` describes the original delivery, not this reporter. State is stored locally and bound to the selected shop and QQ destination. No checkbox, note, or delivery flag is written to Heyinwu.

Keep the state file across restarts. Deleting it establishes a new baseline and skips currently retained history. Run one process per state file. A `.lock` file prevents concurrent local processes; after a crash, verify the old process is stopped before removing its stale lock. Watch-mode poll errors are logged and retried on the next polling interval without exiting. Failed records remain uncheckpointed, so delivery resumes from the first pending record; a persistently failing record blocks later records to preserve ordering. State errors are also logged and retried without resetting state. Startup errors (including configuration, initial login, and lock acquisition) and one-shot command errors still exit nonzero. Ctrl+C or SIGTERM shuts down and releases the lock.

Delivery is **at least once**, not exactly once: a crash after QQ accepts a message but before checkpointing can duplicate it. Records containing images are sent as one Markdown message: original text, two linebreaks, then dimensioned inline images separated by two linebreaks. All image dimensions are resolved before sending, so a metadata failure sends nothing. Image-only records have no leading blank lines or placeholder. Text-only records remain plain text. API retention is 30 days, so longer outages can lose expired records. Offset pagination is not an atomic snapshot; newly inserted records are picked up on subsequent polls.

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

The admin HTTP client allowlists only `login`, `shop_list`, `tell_log_list`, and `tell_log_detail`. All use JSON POST because that is the site's protocol. No browser automation, logout, moderation, configuration changes, note updates, or `done` updates. Login creates a session; production authentication may have server-side audit/session effects. Admin redirects are rejected to avoid forwarding credentials. QQ requests use the official SDK. Its verbose internal logging is disabled because v0.2.1 logs credentials and tokens; returned SDK errors are logged at the poll boundary instead.

The existing `.env` is left unchanged. QQ delivery has been exercised using an isolated HTTP transport, not a real group. To start live forwarding, supply QQ credentials and the bot-specific destination, then explicitly use `-mode watch`.

Image delivery uses the same custom Markdown inline-image syntax as `rmj-card-go`: `msg_type: 2` with `markdown.content` containing `![播报图片 #WIDTHpx #HEIGHTpx](public-image-url)`. Dimensions come from the source image header (PNG/JPEG/GIF), read with a 15-second timeout and 1 MiB metadata limit, rather than reusing the reference card's fixed dimensions. Image-only records do not send a placeholder text message. The bot needs custom Markdown permission, and QQ must be able to fetch the source URL. No `/files` upload or type-7 media message is used. Unlike interactive card replies, proactive broadcasts have no reply `msg_id` or card-command keyboard. Offline SDK coverage verifies actual dimensions in Markdown and absence of placeholder text; QQ rendering still requires live confirmation.

## Research sources

- [Heyinwu admin page](https://heyinwu.top/admin.html) loads the first-party [admin JavaScript](https://heyinwu.top/res/js/main.js?v=20261117d). `use.login` posts `{id, pass, token:""}` to `https://wechat.heyinwu.top/api/web/login`; successful replies contain `data.token` and `data.shop_id`.
- The same script's `http.api_post` injects `token` into the JSON body; application code `455` means the session expired. The client reauthenticates once and retries the read.
- `tell_func.load` uses `tell_log_list` with `{shop_id,page,size,kw:""}`, returning `data.list` and `data.total`. `tell_func.detail` uses `tell_log_detail` with `{shop_id,rid}`, returning `msg` and `imgs`. Records are retained for 30 days. `tell_log_note` writes notes/checkmarks and is deliberately excluded.
- Live read-only verification authenticated the supplied account, read its shop list, fetched two broadcast summaries, and fetched one matching detail. Times are Unix milliseconds; records have stable string RIDs. No admin write endpoint was invoked.
- QQ text reporting follows `../rmj-card-go/qqbot.go`'s proactive `PostGroupMessage` pattern. Images match its `/card` inline Markdown method (`replyCommands` / `commandReply`), without card-specific buttons or reply context. Wire authentication and routes come from Tencent's official [botgo token implementation](https://github.com/tencent-connect/botgo/blob/v0.2.1/token/token_source.go), [group routes](https://github.com/tencent-connect/botgo/blob/v0.2.1/openapi/v1/resource.go), [authorization](https://github.com/tencent-connect/botgo/blob/v0.2.1/openapi/v1/openapi.go), and [message DTOs](https://github.com/tencent-connect/botgo/blob/v0.2.1/dto/message_create.go).

## Verification

Offline regression coverage includes baseline suppression, pagination across 55 new records, oldest-first delivery, failed-send retry, restart deduplication, destination mismatch rejection, and watch-loop recovery. `TestQQSDKTextImagesAndErrorDetails` exercises the real botgo HTTP stack against a local server, checking text plus two Markdown images and preservation of HTTP status, QQ error body, trace ID, and failing operation. No real QQ message was sent; live platform acceptance remains unverified.

`go run . -mode fetch` succeeded against production using the existing `.env`: 95 available records, 50 full records decoded, including 12 with image URLs. No real QQ message was sent.

Container/CI checks: `actionlint .github/workflows/container-image.yml`, `sh -n docker-entrypoint.sh`, and a Linux amd64 static cross-build with the Dockerfile's build flags passed. CLI help was exercised without contacting production. A local Docker image build was not run because Docker is unavailable; the GitHub-hosted workflow still needs its first run.

The reported rejection for RID `1794937669384-3273` was inspected through read-only `tell_log_detail`: it is a text record with 38 characters and no images. Thus that record's rejection occurs on text sending, not the Markdown image path. The old transport discarded the response body, so the historical HTTP 400 alone cannot identify QQ's reason. SDK errors now include the rejected operation (`send QQ text` or `send QQ image N`), QQ response body/code, and trace ID. The next naturally occurring failure supplies the missing evidence without sending a diagnostic production message.
