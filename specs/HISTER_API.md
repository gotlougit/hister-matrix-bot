# Hister API Reference (Ingestion + Search)

This document describes the server endpoints relevant to:

- querying the search index
- adding elements into the index
- optionally writing search-history records into the SQL database

## Base URL

Use your configured Hister server URL (for example `http://localhost:8080`).

## 1. Add Documents to the Search Index

Primary ingestion endpoint:

- `POST /add`

Behavior:

- Accepts JSON or form data
- Processes the document
- Writes into the Bleve search index
- Returns `201 Created` on success

JSON request body fields:

- `url` (required)
- `title` (optional but recommended)
- `text` (optional; if missing, server may extract from HTML/content)
- other document fields are tolerated, but these are the core fields used by ingestion flows

### Example (recommended for services)

```bash
curl -X POST "http://localhost:8080/add" \
  -H "Content-Type: application/json" \
  -d '{
    "url": "https://example.com/article",
    "title": "Example Article",
    "text": "This is indexable content for search."
  }'
```

Expected success response:

- HTTP status: `201 Created`

## 2. Query the Search Index

Two query entry points exist:

- `GET /search` as a WebSocket endpoint (primary live search API)
- `GET /?q=...` (page route that also performs search logic server-side)

### 2.1 WebSocket Search (`/search`)

Connect a WebSocket to:

- `ws://<host>/search` (or `wss://` when TLS is used)

Send JSON messages like:

```json
{"text":"golang templates","highlight":"HTML"}
```

Response is JSON containing search results, duration, and optional history/suggestion fields.

### 2.2 Query via Root Route (`/?q=...`)

The root route also triggers index lookup when `q` is present:

- `GET /?q=golang`

This is primarily a web UI flow, not the preferred machine-to-machine API.

## 3. Write History Records to SQL DB (Optional)

Endpoint:

- `POST /history`

Purpose:

- Stores or updates query-to-URL/title usage records in the SQL database
- Useful for priority results/autocomplete behavior

JSON request body:

- `url`
- `title`
- `query`
- `delete` (boolean; optional)

### Example

```bash
curl -X POST "http://localhost:8080/history" \
  -H "Content-Type: application/json" \
  -d '{
    "url": "https://example.com/article",
    "title": "Example Article",
    "query": "example",
    "delete": false
  }'
```

## 4. Endpoint Summary

- `POST /add`: ingest documents into the search index (Bleve)
- `GET /search` (WebSocket): query the search index
- `GET /?q=...`: UI route that also performs index search
- `POST /history`: write/update history records in SQL DB (optional, not required for indexing)

## 5. Minimal Ingestion Contract for External Services

If your service only needs indexing, implement:

1. Send `POST /add` with JSON body containing at least `url`.
2. Treat HTTP `201` as success.
3. Optionally include `title` and `text` for better relevance and faster indexing quality.
4. Optionally call `POST /history` if you want query-priority behavior in Hister UI.
