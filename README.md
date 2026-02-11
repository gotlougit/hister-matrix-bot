# Hister Matrix Bot (PoC)

Matrix bot that listens in allowlisted rooms, indexes URLs into Hister, and replies to search triggers with threaded top results.

## What it does

- Listens to `m.room.message` events (including encrypted rooms when crypto is initialized).
- Extracts and indexes unique `http://` / `https://` URLs via Hister `POST /add`.
- Handles search triggers:
  - `/search <term>`
  - `@bot <term>`
  - `<term> @bot`
- Replies in-thread with compact top results from Hister WebSocket `/search`.

## Requirements

- Go 1.23+
- Matrix user access token for the bot account
- Reachable Hister backend (`/add`, `/search`)

This project is configured and tested with the pure-Go olm stack (`goolm`) to avoid requiring system `libolm` headers.

## Quick start

1. Create config file (example below).
2. Run:

```bash
CGO_ENABLED=0 go run -tags goolm ./cmd/bot -config ./config.yaml
```

You can also set config path via env var:

```bash
MATRIX_BOT_CONFIG=./config.yaml CGO_ENABLED=0 go run -tags goolm ./cmd/bot
```

## Config

`config.yaml`:

```yaml
matrix:
  homeserver_url: "https://matrix.example.org"
  user_id: "@bot:example.org"
  access_token: "REDACTED"
  device_id: "BOTDEVICE1"         # optional; if omitted bot resolves with /account/whoami
  bot_display_name: "bot"
  sync_timeout_ms: 30000
  allowed_room_ids:
    - "!abc123:example.org"

bot:
  search_command: "/search"
  max_results: 5
  reply_mode: "thread"
  max_query_len: 200

hister:
  base_url: "http://localhost:8080"
  add_path: "/add"
  search_ws_path: "/search"

http:
  request_timeout_ms: 10000

storage:
  state_db_path: "./data/state.db"
  crypto_db_path: "./data/crypto.db"
```

## Behavior details

- Ignores bot-authored messages.
- Ignores rooms not in `matrix.allowed_room_ids`.
- URL indexing failures are logged and do not stop message handling.
- Invalid/too-long queries return: `Invalid search query.`
- Search backend failures return: `Search failed, please try again.`

## E2EE notes

- Crypto helper is initialized at startup; startup fails if crypto init fails.
- Set `MATRIX_PICKLE_KEY` to control pickle key used by crypto store encryption.
- If `MATRIX_PICKLE_KEY` is unset, a key is derived from the access token.

## Development

Run tests:

```bash
CGO_ENABLED=0 go test -tags goolm ./...
```

Main packages:

- `cmd/bot/main.go` - wiring and startup
- `internal/bot` - message handling flow
- `internal/matrix` - mautrix adapter
- `internal/hister` - Hister HTTP/WS client
- `internal/triggers` - trigger/url parsing
- `internal/storage` - sqlite persistence
- `internal/config` - YAML config loading/validation
