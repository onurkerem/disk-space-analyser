# Disk Space Analyser

A Go CLI that scans your filesystem, computes per-directory sizes, stores results in SQLite, and serves an interactive web report. Runs as a background daemon — start it, close your terminal, come back to the report when the scan finishes.

## Features

- **Concurrent scanning** — worker pool traverses directories in parallel, coordinator computes subtree sizes bottom-up
- **Incremental rescans** — skips unchanged directories via mtime comparison; only rescans what changed
- **Shallow scanning** — auto-treats known heavy dirs (`.git`, `node_modules`, `venv`, `.next`, etc.) as leaf nodes
- **Persistent storage** — SQLite with WAL mode, no external dependencies
- **Web report** — served at `http://localhost:3097/report` with collapsible tree view and largest-directories summary
- **JSON API** — `/api/summary` and `/api/tree` endpoints for programmatic access
- **Single binary** — pure Go, no CGO required

## Prerequisites

- Go 1.25+
- macOS or Linux

## Build

```bash
git clone https://github.com/onurkerem/disk-space-analyser.git
cd disk-space-analyser
go build -o disk-space-analyser ./cmd/disk-space-analyser
```

## Usage

```bash
./disk-space-analyser start           # scan entire filesystem
./disk-space-analyser start /home     # scan a specific path
./disk-space-analyser stop            # stop the daemon
./disk-space-analyser status          # show daemon and last scan status
./disk-space-analyser clear           # clear all scan data from the database
```

After starting, the daemon scans in the background and starts an HTTP server. View the report at:

```
http://localhost:3097/report
```

## API

| Endpoint | Description |
|----------|-------------|
| `GET /api/summary?top=N` | Largest N directories (default 20) |
| `GET /api/tree?path=/&offset=0&limit=50` | Children of a directory with pagination |
| `GET /report` | Web report UI |

## Architecture

```
cmd/disk-space-analyser/   CLI entry point, daemon fork, signal handling
internal/
  scanner/                 Worker pool, coordinator, batched writer, shallow/incremental scan
  db/                      SQLite persistence, schema, queries
  daemon/                  PID file, status file, data directory management
  server/                  HTTP server, JSON API, web report
  fmt/                     FormatBytes utility
```

Data lives in `~/.disk-space-analyser/` (SQLite DB, PID file, status, logs).

## Testing

```bash
go test ./...
```

## License

MIT
