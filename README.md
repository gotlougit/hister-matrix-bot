# Hister Matrix Bot

Matrix bot that listens in allowlisted rooms, indexes URLs into [Hister](https://github.com/asciimoo/hister), and replies to search triggers with threaded top results.

## Disclaimer

This is a vibe coded experiment! It is meant to be secure, but MAY NOT be!
Self-host it, with as little permission as possible granted to it.
Be prepared to wipe it off the face of the Earth if needed.

## What it does

- Listens to `m.room.message` events (including encrypted rooms when crypto is initialized).
- Extracts and indexes unique `http://` / `https://` URLs via Hister `POST /add`.
- Handles search triggers:
  - `/search <term>`
  - `@bot <term>`
  - `<term> @bot`
- Replies in-thread with compact top results from Hister WebSocket `/search`.
- Handles `/catchmeup` by summarizing recent room chat with an LLM.

## Requirements

- Go 1.23+
- Matrix user access token for the bot account
- Reachable Hister backend (`/add`, `/search`)
- LLM API credentials:
  - `OPENAI_API_KEY`
  - `OPENAI_BASE_URL`

This project is configured and tested with the pure-Go olm stack (`goolm`) to avoid requiring system `libolm` headers.

## Service Setup Flow

### 1. Create `config.yaml`

Use this as a starting point:

```yaml
matrix:
  homeserver_url: "https://matrix.example.org"
  user_id: "@bot:example.org"
  access_token: "REDACTED"
  device_id: "BOTDEVICE1" # optional; if omitted bot resolves via /account/whoami
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
  state_db_path: "/var/lib/hister-matrix-bot/state.db"
  crypto_db_path: "/var/lib/hister-matrix-bot/crypto.db"
```

### 2. Create environment file for secrets/runtime overrides

Example `/etc/hister-matrix-bot/bot.env`:

```bash
MATRIX_BOT_CONFIG=/etc/hister-matrix-bot/config.yaml
MATRIX_PICKLE_KEY=replace-with-random-secret
# For automatic new-device recovery (first time ONLY!):
MATRIX_BOT_PASSWORD=replace-with-bot-password

# Optional for automatic trust bootstrap:
# MATRIX_RECOVERY_KEY=...
# or
# MATRIX_RECOVERY_PASSPHRASE=...

# MATRIX_BOT_NEW_DEVICE_ID=BOTDEVICE2

# Required for /catchmeup LLM summaries:
OPENAI_API_KEY=replace-with-api-key
OPENAI_BASE_URL=https://your-llm-endpoint.example/v1
```

### 3. First startup and crypto bootstrap behavior

Run locally once before enabling service:

```bash
MATRIX_BOT_CONFIG=/etc/hister-matrix-bot/config.yaml CGO_ENABLED=0 go run -tags goolm ./cmd/bot
```

Startup flow:

- Initializes Matrix crypto machine.
- If crypto store says "not shared" but server already has keys, bot auto-repairs local crypto account state and retries.
- If that still fails due to device/key conflict and `MATRIX_BOT_PASSWORD` is set, bot logs in as a fresh device and resets local device-scoped crypto state.
- If `MATRIX_RECOVERY_KEY` or `MATRIX_RECOVERY_PASSPHRASE` is set, bot verifies/signs its own device during startup.

Important:

- If fresh-device recovery happens, startup logs the new `access_token` and `device_id`.
- Persist those new values into `matrix.access_token` and `matrix.device_id` in config/secrets before restart, otherwise later restarts may drift.

### 4. Run as systemd service

Example unit file `/etc/systemd/system/hister-matrix-bot.service`:

```ini
[Unit]
Description=Hister Matrix Bot
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory=/opt/hister-element-bot/bot
EnvironmentFile=/etc/hister-matrix-bot/bot.env
ExecStart=/usr/bin/env CGO_ENABLED=0 /usr/local/bin/hister-matrix-bot
Restart=always
RestartSec=5s

[Install]
WantedBy=multi-user.target
```

Build/install binary example:

```bash
CGO_ENABLED=0 go build -tags goolm -o /usr/local/bin/hister-matrix-bot ./cmd/bot
```

## Local run (without systemd)

With explicit config path:

```bash
CGO_ENABLED=0 go run -tags goolm ./cmd/bot -config ./config.yaml
```

With env-configured path:

```bash
MATRIX_BOT_CONFIG=./config.yaml CGO_ENABLED=0 go run -tags goolm ./cmd/bot
```

## Runtime behavior

- Ignores bot-authored messages.
- Ignores rooms not in `matrix.allowed_room_ids`.
- URL indexing failures are logged and do not stop message handling.
- Invalid/too-long queries return: `Invalid search query.`
- Search backend failures return: `Search failed, please try again.`
- `/catchmeup` fetches up to 40 text messages from the last 24 hours in the room, formats them as `Speaker: message`, sends them to the configured LLM, and replies with the generated summary.

## E2EE notes

- Crypto helper initialization happens at startup; startup fails if crypto init cannot be recovered.
- `MATRIX_PICKLE_KEY` controls pickle key used by crypto store encryption.
- If `MATRIX_PICKLE_KEY` is unset, key is derived from the Matrix access token.

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
