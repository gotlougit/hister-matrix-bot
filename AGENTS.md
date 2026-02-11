# AGENTS.md

## Project

Hister Matrix Bot proof of concept.

The bot:
- Listens to Matrix `m.room.message` events (including encrypted rooms when crypto is initialized).
- Extracts unique `http://` and `https://` URLs and indexes them via Hister `POST /add`.
- Handles search triggers:
  - `/search <term>`
  - `@bot <term>`
  - `<term> @bot`
- Replies in-thread with compact top results from Hister WebSocket `/search`.

## Requirements

- Go `1.23+`
- Matrix bot account access token
- Reachable Hister backend with `/add` and `/search`

Use pure-Go olm (`goolm`) and keep `CGO_ENABLED=0` in local commands unless intentionally changing crypto/toolchain behavior.

## Run

With explicit config path:

```bash
CGO_ENABLED=0 go run -tags goolm ./cmd/bot -config ./config.yaml
```

With env-configured path:

```bash
MATRIX_BOT_CONFIG=./config.yaml CGO_ENABLED=0 go run -tags goolm ./cmd/bot
```

## Config Contract

Expected config file sections:
- `matrix`
- `bot`
- `hister`
- `http`
- `storage`

Important fields by section:
- `matrix`: `homeserver_url`, `user_id`, `access_token`, optional `device_id`, `bot_display_name`, `sync_timeout_ms`, `allowed_room_ids`
- `bot`: `search_command`, `max_results`, `reply_mode`, `max_query_len`
- `hister`: `base_url`, `add_path`, `search_ws_path`
- `http`: `request_timeout_ms`
- `storage`: `state_db_path`, `crypto_db_path`

## Runtime Behavior

- Ignore bot-authored messages.
- Ignore rooms not in `matrix.allowed_room_ids`.
- URL indexing failures must be logged and must not stop message handling.
- Invalid or too-long query response: `Invalid search query.`
- Search backend failure response: `Search failed, please try again.`

## E2EE Notes

- Crypto helper initialization happens at startup; startup fails if crypto init fails.
- `MATRIX_PICKLE_KEY` controls pickle key used by crypto store encryption.
- If `MATRIX_PICKLE_KEY` is unset, derive key from the access token.

## Development

Run tests with:

```bash
CGO_ENABLED=0 go test -tags goolm ./...
```

Main package layout:
- `cmd/bot/main.go`: wiring and startup
- `internal/bot`: message handling flow
- `internal/matrix`: mautrix adapter
- `internal/hister`: Hister HTTP/WS client
- `internal/triggers`: trigger/url parsing
- `internal/storage`: sqlite persistence
- `internal/config`: YAML config loading/validation

## Agent Checklist

When changing behavior, keep these invariants unless intentionally updating product requirements:
- Respect room allowlist filtering.
- Do not process self-authored messages.
- Preserve failure-tolerant URL indexing path.
- Keep user-facing error strings stable if tests or clients depend on them.
- Keep `goolm` compatibility in run/test commands.
