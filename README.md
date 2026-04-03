# Disk Space Analyser

A Go CLI that scans your filesystem, computes per-directory sizes, stores results in SQLite, and serves an interactive web report. Runs as a background daemon — start it, close your terminal, come back when it's done.

The web report is available **immediately**, even while scanning is still in progress.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/onurkerem/disk-space-analyser/main/install.sh | bash
```

This checks for Go (installs it via Homebrew on macOS if missing), clones the repo, builds the binary, and symlinks `dsa` to `~/.local/bin`. After installation, `dsa` works from any directory.

### Manage an existing installation

Run the installer again to get options:

```
1) Reinstall from scratch (remove everything, clone fresh)
2) Update source and rebuild (keep data)
3) Uninstall completely
```

## Quickstart

```bash
dsa start /path/to/scan
```

Open [http://localhost:3097/report](http://localhost:3097/report) — the tree view populates as directories are scanned.

## Commands

```
dsa start [path]    Start daemon (default: /)
dsa stop            Stop running daemon
dsa status          Show daemon state and last scan info
dsa clear           Clear all scan data (stops daemon first)
```

## How It Works

1. `start` forks a background daemon process
2. The HTTP server starts immediately at `:3097`
3. A concurrent scan runs with a worker pool, writing directory sizes to SQLite
4. Progress is streamed to the web UI via a status banner
5. When the scan finishes, the full report is ready — the server keeps running

**Block-based sizes** — uses `stat.Blocks × 512` instead of apparent file size, so sparse files (Docker VM images, etc.) report their real disk usage.

## Features

- **Concurrent scanning** — worker pool traverses directories in parallel
- **Incremental rescans** — skips unchanged directories via mtime comparison
- **Shallow scanning** — `.git`, `node_modules`, `venv`, etc. are treated as leaf nodes
- **Real-time progress** — web UI shows a scanning banner while the scan runs
- **Persistent storage** — SQLite with WAL mode, no external dependencies
- **Single binary** — pure Go, no CGO

## API

| Endpoint | Description |
|----------|-------------|
| `GET /report` | Interactive web report (tree view + top directories) |
| `GET /api/summary?top=N` | Largest N directories (default 20) |
| `GET /api/tree?path=/&offset=0&limit=50` | Paginated children of a directory |
| `GET /api/meta` | Scan root path |
| `GET /api/status` | Current scan state (scanning/complete, dirs indexed, errors) |

## Project Structure

```
cmd/disk-space-analyser/    CLI entry point, daemon fork, signal handling
internal/
  scanner/                  Worker pool, coordinator, batched writer
  db/                       SQLite persistence, schema, queries
  daemon/                   PID/status file, data directory management
  server/                   HTTP server, JSON API, embedded web UI
  fmt/                      Byte formatting utilities
```

Data lives in `~/.disk-space-analyser/`.

## Build from Source

```bash
git clone https://github.com/onurkerem/disk-space-analyser.git
cd disk-space-analyser
go build -o dsa ./cmd/disk-space-analyser
go test ./...
```

Requires Go 1.25+. macOS or Linux.

## License

MIT
