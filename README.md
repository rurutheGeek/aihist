# aihist

AI ツールの会話履歴を1か所に集め、検索・閲覧し、別の AI へ渡せる Markdown に書き出す。

## 対応状況

| ツール | 読む場所 | 状態 |
|---|---|---|
| Claude Code | `~/.claude/projects/**/*.jsonl`（サブエージェント含む） | 済 |
| OpenCode 1.18 | `~/.local/share/opencode/opencode.db` | 済 |
| Antigravity | `%USERPROFILE%\.gemini\antigravity-ide\conversations\*.db` | 済 |
| Codex | `~/.codex/sessions/**/rollout-*.jsonl`、題名は `session_index.jsonl` | 済 |
| Open WebUI | REST API `/api/v1/chats` | 未 |

## 使い方

```bash
go build -o bin/aihist ./cmd/aihist
bin/aihist serve -addr :8080 -token <トークン>
bin/aihist push -server http://<サーバー>:8080 -token <トークン>
bin/aihist search 引き継ぎ
bin/aihist export -thinking <会話IDの先頭>
```

| 入れ方 | やること |
|---|---|
| `curl … \| sh` | 各マシンで1行。サーバーが配る `/api/install.sh`（Windows は `/api/install.ps1`）が aihist を入れて `push` する（下記） |
| `push` | 各マシンで実行する。Claude Code・Codex・Antigravity の既定の置き場所から、サーバーに無いか変わったファイルだけを送る。`-every 10m` で送り続ける |
| 画面の `↑` | ファイルを選んで上げる。中身から Claude Code・Codex・Antigravity を見分ける |
| `import <ツール> [場所]` | サーバーと同じマシンの記録を直接読む |

`push` は OpenCode の DB そのものは送らない（操作ログで数 GB あり、ログイン情報も入っている）。会話ごとに `session`・`message`・`part` と `project` の行を加工せずに書き出した `<session id>.json` を原本として送る。

```bash
curl -fsSL https://<サーバー>/api/install.sh | AIHIST_TOKEN=<トークン> sh          # Linux・WSL・Mac
$env:AIHIST_TOKEN="<トークン>"; irm https://<サーバー>/api/install.ps1 | iex      # Windows
```

実行ファイルは `serve -bin <dir>` に置いた `aihist-<os>-<arch>` をトークン付きで配る。WSL ではホスト名に `-wsl` を付ける。

送られたファイルは原本として `-raw`（既定 `~/.local/share/aihist/raw/<ホスト>/<ツール>/`）に残る。アダプタを直したら原本から取り込み直せる。

DB は既定で `~/.local/share/aihist/aihist.db`（`-db` で変更）。取り込みは会話ごとの丸ごと置き換えで、元データが変わっていない会話は飛ばす。トークンは `AIHIST_TOKEN` でも渡せ、`push` が使う `/api` にだけ要る。画面（アップロードを含む）は前に置く認証（SSO など）で守る。

検索は SQLite FTS5 の trigram。3文字未満の語は `LIKE` で探す。

## 元の AI へ戻す

```bash
bin/aihist restore -server https://<サーバー> -token <トークン> <会話IDの先頭>
```

原本をサーバーから取り出し、元のツールが読む場所へ戻して、続けるコマンドを表示する。手元に中身の違う同じ会話があるときは上書きしない（`-force` で上書き）。

| ツール | 戻す先 | 続け方 |
|---|---|---|
| Claude Code | `~/.claude/projects/<作業フォルダ>/<会話>.jsonl`（サブエージェントも） | `claude --resume <会話>` |
| Codex | `~/.codex/sessions/年/月/日/rollout-*.jsonl`、題名は `session_index.jsonl` | `codex resume <会話>` |
| OpenCode | `opencode.db` へ行を書き戻す（同じ ID は上書き） | `opencode --session <会話>` |
| Antigravity | `conversations\<会話>.db` | Antigravity の一覧から開く |

Codex と OpenCode は、手元から消した会話を戻して続きを話せることを確かめた。Claude Code は戻したファイルが原本とバイト単位で同じことまで確かめた。Antigravity は未確認。

## 構成

```
cmd/aihist/                 CLI（import / serve / search / export）
internal/model/             ツールに依存しない内部モデル（会話・メッセージ・パート）
internal/store/             SQLite・全文検索
internal/adapter/<tool>/    ツールごとの読み取り
internal/ingest/            変わった会話だけを取り込む
internal/raw/               送られた原本の置き場所
internal/push/              手元の元データを差分だけ送る
internal/export/            Markdown 書き出し
internal/web/               画面
```

## Codex の構造

`response_item` がモデルとの入出力。user 役の `response_item` には注入された文脈が混ざるので、利用者の発言は `event_msg` の `UserMessage` から取る。思考は暗号化されていて、読めるのは要約だけ。長い会話は `rollout-..._<n>.jsonl` に分かれ、`session_meta.id` が同じものを1会話にまとめる。サブエージェントは `parent_thread_id` を持ち、`subagent_history_start_ordinal` より前は親の履歴の写し。

## Antigravity の構造

会話ごとに SQLite が1つ。`steps` の1行が1ステップで、`step_payload` は protobuf（`.proto` は非公開なので番号は実物から割り出した）。

| step_type | 本体 | 中身 |
|---|---|---|
| 14 | `.19` | ユーザー発言（`.19.2` 本文） |
| 15 | `.20` | モデル返答（`.20.1` 本文、`.20.3` 思考、`.20.7` ツール呼び出し：`.1` ID・`.2` 名前・`.3` 引数 JSON） |
| 8 | `.14` | view_file（`.14.1` パス、`.14.4` 内容） |
| 21 | `.28` | run_command（`.28.23` コマンド、`.28.21.1` 出力、`.28.2` 作業ディレクトリ） |
| 7 / 9 / 5 / 17 | `.13` / `.15` / `.10` / `.24` | grep_search / list_dir / write_to_file / エラー |
| 23 / 98 | `.30` / `.111` | 途中の要約 / 会話の要約 |

共通の `.5` は時刻（`.5.1`）と会話 ID（`.5.20.4`）。`trajectory_metadata_blob` に作業フォルダ・リポジトリ・ブランチ。
`conversations\*.pb`（対応する `.db` の無い古い会話）は暗号化されていて読めない。

## まだ無いもの

- 原本からの一括の取り込み直し（今は原本が変わったときだけ）
- 常駐の仕組み（Windows のタスク・systemd）
- 秘密値の伏せ字
- MCP サーバー、Claude Code へのネイティブ復元
