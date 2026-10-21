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

In **Settings → Machines**, enter a machine name and press **Add machine** to get a one-line install command (shown once; use **Regenerate** on the machine's page if you need it again — the old command stops working). It sends once right away, then on the schedule from Settings. Messages are in Japanese on Japanese systems and English otherwise.

```bash
curl -fsSL https://<server>/api/install.sh | AIHIST_TOKEN=<token> AIHIST_HOST=<name> sh   # Linux, macOS, WSL
```

```bat
powershell -NoProfile -ExecutionPolicy Bypass -Command "irm https://<server>/api/install.ps1 | iex; Install-Aihist -Token <token> -Name <name>"
```

The Windows line works in both Command Prompt and PowerShell. Connect AI apps such as Claude Code in **Settings → Connected apps**.

Nothing stays resident. The OS timer (systemd user timer or cron, launchd, Task Scheduler) wakes `aihist push -tick` every minute; it exits right away unless it is time to send. When to send, which AIs, attachments, what to remove before sending, and stored data cleanup are chosen in **Settings** (bottom left), globally and per machine; machines pick up changes within a few minutes. Only changed files are sent; a typical run takes a few seconds and ~10 MB of memory. Before sending, the client compares itself with the binary the server distributes and updates itself, so supporting a new tool only needs a server update.

**Settings → Sending → Keep on the machine** deletes local conversation files (Claude Code, Codex, Antigravity) that were last modified before the chosen number of days and whose identical copy (same size and mtime) is already on the server. Indexes and the OpenCode database are never touched. Use `aihist restore` to bring one back.

**Settings → Sending → Hide** replaces the chosen kinds (tokens, passwords and private keys; email addresses; phone numbers; postal addresses; credit card numbers; IP addresses) with markers such as `[トークン]` on the machine before sending. Only tokens are removed by default. Local files are not changed (a conversation restored from the server keeps the markers). Uploads have the same choice under **Settings → Upload**, and **Settings → Storage → Hide in saved chats too** hides them in what is already saved (this can't be undone). Detection uses fixed patterns, so some addresses may be missed. The Antigravity database can't be rewritten and is sent as is.

| Variable | Meaning |
|---|---|
| `AIHIST_EVERY` | `0` sends once and installs no timer |
| `AIHIST_HOST` | Machine name (default: host name; `-wsl` is appended in WSL) |
| `AIHIST_TOOLS` | Initial choice of AIs to send (comma-separated `claude_code,codex,antigravity,opencode`). If unset, the installer lists the ones found on the machine and asks |
| `AIHIST_ATTACHMENTS` | Initial choice for attachments (images, PDFs, audio…). When off, embedded files are replaced with a tiny file of the same kind (restored conversations still load, without the content; Antigravity databases are sent as-is). If unset, the installer asks |
| `AIHIST_CLEANUP` | Days after which conversations already on the server are deleted locally (overrides Settings → Sending → Keep on the machine; `0` keeps everything) |
| `AIHIST_AUTO_UPDATE` | `0` disables self-update |
| `AIHIST_BIN_DIR` | Where to put the binary (default `~/.local/bin`) |

Uninstall removes the binary, the config and the timer (conversations stay on the server):

```bash
curl -fsSL https://<server>/api/uninstall.sh | sh
powershell -NoProfile -ExecutionPolicy Bypass -Command "irm https://<server>/api/uninstall.ps1 | iex"   # Windows
```

You can also drop files on the **Upload** page: Claude Code `.jsonl`, Codex `rollout-*.jsonl`, Antigravity `.db`, and the ChatGPT / claude.ai export `.zip`.

## Organize with AI (tags, summaries, knowledge)

Adds tags and a summary to chats, and drafts "knowledge" — short write-ups of problems that took real effort to solve. Nothing runs automatically.

- **API**: enter an OpenAI-compatible endpoint, key, and model in **Settings → AI**. It's called only when you press ✦ on a chat, "Organize with AI" on selected chats, or the option on the upload page.
- **Prompts**: save prompts and Skills you use often and use them from AIs connected over MCP (served as MCP prompts, also readable with `list_prompts` and `get_prompt`). Skills that Claude Code loaded are found in your history under "Skills in history", ready to save.
- **MCP**: connect your own AI (Claude Code and others) to `<server>/api/mcp` with a token (pick the app in **Settings → Connected apps** to get its setup with the token filled in). Your AI reads the chats and writes the results back. It can also delete, archive, pin, rename, and retag chats, or look through the chat list and create tags, when you ask. Chats started by programs or AIs (`opencode run`, `claude -p`, `codex exec`, and so on) are listed under "Automated" and skipped when organizing.
- Tags are yours (**Settings → Tags**, with templates). Each chat is read in its own requests; long chats are read in pages with running notes.
- Knowledge is kept only when the AI's quoted evidence of the struggle appears verbatim in the chat and its score is 4 or more. Drafts warn about machine names, folder names, URLs, and IPs; you decide what to register.
- **Profile**: turn on "Add to it when organizing with AI" to keep adding one-line notes about who you are, taken from what you plainly say about yourself (not drafts, hypotheticals, other people, or one-off requests), split into "About you" and "How you want AI to respond". Write what not to extract in "Instructions". Edit any line, copy it as Markdown for other AIs, or read it over MCP with `get_profile`.

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
