# aihist

A self-hosted hub for the conversation history of AI coding tools. It collects conversations from Claude Code, Codex, OpenCode and Antigravity (plus ChatGPT and claude.ai exports), lets you search and browse them in a chat-style UI, exports a conversation as Markdown for another AI, and restores a conversation back into the original tool so you can keep going.

[日本語](README.md)

## Run the server

```bash
curl -fsSLO https://raw.githubusercontent.com/rurutheGeek/aihist/main/compose.yaml
docker compose up -d        # http://localhost:8080
```

The image is `ghcr.io/ruruthegeek/aihist` (linux/amd64, arm64). Data lives in `/data`. The UI has no login of its own; put SSO or another auth proxy in front of it and let `/api/*` through (the API is protected by tokens).

## Send from each machine

Open **Settings** (bottom left), enter a machine name and press **Add machine**. You get a token (shown once) and a one-line command:

```bash
curl -fsSL https://<server>/api/install.sh | AIHIST_TOKEN=<token> sh      # Linux, macOS, WSL
$env:AIHIST_TOKEN="<token>"; irm https://<server>/api/install.ps1 | iex  # Windows
```

Nothing stays resident. The OS timer (systemd user timer or cron, launchd, Task Scheduler) runs `aihist push` every 10 minutes and only changed files are sent; a typical run takes a few seconds and ~10 MB of memory. Before sending, the client compares itself with the binary the server distributes and updates itself, so supporting a new tool only needs a server update.

**Settings → Local cleanup** deletes local conversation files (Claude Code, Codex, Antigravity) that were last modified before the chosen number of days and whose identical copy (same size and mtime) is already on the server. Indexes and the OpenCode database are never touched. Use `aihist restore` to bring one back.

| Variable | Meaning |
|---|---|
| `AIHIST_EVERY` | Interval (default `10m`, `0` = send once) |
| `AIHIST_HOST` | Machine name (default: host name; `-wsl` is appended in WSL) |
| `AIHIST_CLEANUP` | Days after which conversations already on the server are deleted locally (overrides Settings → Local cleanup; `0` keeps everything) |
| `AIHIST_AUTO_UPDATE` | `0` disables self-update |
| `AIHIST_BIN_DIR` | Where to put the binary (default `~/.local/bin`) |

Uninstall removes the binary, the config and the timer (conversations stay on the server):

```bash
curl -fsSL https://<server>/api/uninstall.sh | sh
irm https://<server>/api/uninstall.ps1 | iex     # Windows
```

You can also drop files on the **Upload** page: Claude Code `.jsonl`, Codex `rollout-*.jsonl`, Antigravity `.db`, and the ChatGPT / claude.ai export `.zip`.

## Continue in the original AI

```bash
aihist restore <conversation id prefix>
```

| Tool | Restored to | Continue with |
|---|---|---|
| Claude Code | `~/.claude/projects/<dir>/<id>.jsonl` | `claude --resume <id>` |
| Codex | `~/.codex/sessions/Y/M/D/rollout-*.jsonl` | `codex resume <id>` |
| OpenCode | rows written back into `opencode.db` | `opencode --session <id>` |
| Antigravity | `conversations/<id>.db` | open it from Antigravity |

## Build

```bash
go build -o bin/aihist ./cmd/aihist
bin/aihist serve -addr :8080
```

## License

MIT
