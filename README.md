# aihist

AI ツールの会話履歴を1か所に集め、検索・閲覧し、別の AI へ渡せる Markdown に書き出す。

## 対応状況

| ツール | 読む場所 | 状態 |
|---|---|---|
| Claude Code | `~/.claude/projects/**/*.jsonl`（サブエージェント含む） | 済 |
| OpenCode 1.18 | `~/.local/share/opencode/opencode.db` | 済 |
| Antigravity | `%USERPROFILE%\.gemini\antigravity-ide\conversations\*.db` | 済 |
| Codex | `~/.codex/sessions/**/rollout-*.jsonl`、題名は `session_index.jsonl` | 済 |
| ChatGPT（Web） | 「データをエクスポート」の zip か `conversations.json` を画面から上げる | 済 |
| claude.ai | 書き出しの zip か `conversations.json` を画面から上げる | 済 |
| Open WebUI | REST API `/api/v1/chats` | 未 |

## サーバーを立てる

```bash
echo "AIHIST_TOKEN=$(openssl rand -base64 32 | tr -d '/+=')" > .env
docker compose up -d        # http://localhost:8080
```

イメージは `ghcr.io/ruruthegeek/aihist`（linux/amd64・arm64）。データは `/data`（`aihist.db` と送られた原本 `raw/`）。各マシンへ配る実行ファイルもイメージに入っている。

画面には認証が無い。外へ出すときは前に SSO などを置き、`/api/*` だけを通す（`/api/*` はトークンが守る）。

コンテナを使わないとき:

```bash
go build -o bin/aihist ./cmd/aihist
AIHIST_TOKEN=<トークン> bin/aihist serve -addr :8080 -bin <配る実行ファイルの置き場所>
```

## 各マシンから送る

画面の「送る設定」に、トークンを埋めたコマンドが出る。

```bash
curl -fsSL https://<サーバー>/api/install.sh | AIHIST_TOKEN=<トークン> sh      # Linux・macOS・WSL
$env:AIHIST_TOKEN="<トークン>"; irm https://<サーバー>/api/install.ps1 | iex  # Windows
```

常駐はしない。OS のタイマー（systemd のユーザータイマー、無ければ cron、macOS は launchd、Windows はタスク スケジューラ）が 10 分ごとに `aihist push` を1回動かす。2回目からは変わったファイルだけを送るので、1回は数秒・メモリ 10 MB ほどで終わる。

送る前に、サーバーが配る実行ファイル（`/api/bin`）と自分の sha256 を比べ、違えば入れ替えてから送る。サーバーを新しくすれば、対応するツールが増えても各マシンでやり直すことは無い。

| 変数 | 意味 |
|---|---|
| `AIHIST_EVERY` | 送る間隔（既定 `10m`。`0` なら今1回送るだけ） |
| `AIHIST_HOST` | このマシンの名前（既定はホスト名。WSL では `-wsl` を付ける） |
| `AIHIST_AUTO_UPDATE` | `0` で自動更新を止める（設定ファイルに書いてもよい） |
| `AIHIST_BIN_DIR` | 実行ファイルの置き場所（既定 `~/.local/bin`。Windows は `%LOCALAPPDATA%\aihist`） |

置くもの: 実行ファイル、設定 `~/.config/aihist/config`（macOS は `~/Library/Application Support/aihist`、Windows は `%APPDATA%\aihist`。トークンが入るので本人だけが読める）、タイマー。

消すときは次のどちらか。置いたものをすべて消す（送った会話はサーバーに残る）。

```bash
curl -fsSL https://<サーバー>/api/uninstall.sh | sh     # ~/.config/aihist/uninstall.sh でもよい
irm https://<サーバー>/api/uninstall.ps1 | iex          # Windows
```

## そのほかの使い方

```bash
bin/aihist push                 # 設定ファイルのサーバーへ1回送る（-server・-token・-host で上書き）
bin/aihist search 引き継ぎ
bin/aihist export -thinking <会話IDの先頭>
```

| 入れ方 | やること |
|---|---|
| `push` | Claude Code・Codex・Antigravity の既定の置き場所と OpenCode の DB から、サーバーに無いか変わったものだけを送る |
| 画面の `↑` | ファイルを選んで上げる。中身から Claude Code・Codex・Antigravity・ChatGPT・claude.ai を見分ける |
| `import <ツール> [場所]` | サーバーと同じマシンの記録を直接読む |

`push` は OpenCode の DB そのものは送らない（操作ログで数 GB あり、ログイン情報も入っている）。会話ごとに `session`・`message`・`part` と `project` の行を加工せずに書き出した `<session id>.json` を原本として送る。

送られたファイルは原本として `-raw`（既定 `~/.local/share/aihist/raw/<ホスト>/<ツール>/`）に残る。アダプタを直したら原本から取り込み直せる。

DB は既定で `~/.local/share/aihist/aihist.db`（`-db` で変更）。取り込みは会話ごとの丸ごと置き換えで、元データが変わっていない会話は飛ばす。

検索は SQLite FTS5 の trigram。3文字未満の語は `LIKE` で探す。一覧は AI・モデル・マシン・プロジェクト（作業フォルダ）・期間で絞れる。

## 元の AI へ戻す

```bash
bin/aihist restore <会話IDの先頭>     # サーバーとトークンは設定ファイルから
```

原本をサーバーから取り出し、元のツールが読む場所へ戻して、続けるコマンドを表示する。手元に中身の違う同じ会話があるときは上書きしない（`-force` で上書き）。

| ツール | 戻す先 | 続け方 |
|---|---|---|
| Claude Code | `~/.claude/projects/<作業フォルダ>/<会話>.jsonl`（サブエージェントも） | `claude --resume <会話>` |
| Codex | `~/.codex/sessions/年/月/日/rollout-*.jsonl`、題名は `session_index.jsonl` | `codex resume <会話>` |
| OpenCode | `opencode.db` へ行を書き戻す（同じ ID は上書き） | `opencode --session <会話>` |
| Antigravity | `conversations\<会話>.db` | Antigravity の一覧から開く |

Codex と OpenCode は、手元から消した会話を戻して続きを話せることを確かめた。Claude Code は戻したファイルが原本とバイト単位で同じことまで確かめた。Antigravity は未確認。ChatGPT と claude.ai は戻せない。

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
