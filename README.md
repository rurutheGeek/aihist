<div align="center">

<img src="docs/images/icon.svg" width="96" alt="">

# aihist

**AI との会話を、ぜんぶ自分の手元に。**

Claude Code・Codex・OpenCode・ChatGPT などの会話を1か所に集め、探して、読み返して、続きから話せるセルフホストのサーバー。

[![最新版](https://img.shields.io/github/v/release/rurutheGeek/aihist?label=%E6%9C%80%E6%96%B0%E7%89%88)](https://github.com/rurutheGeek/aihist/releases/latest)
[![テスト](https://github.com/rurutheGeek/aihist/actions/workflows/test.yml/badge.svg)](https://github.com/rurutheGeek/aihist/actions/workflows/test.yml)
[![ライセンス](https://img.shields.io/github/license/rurutheGeek/aihist?label=%E3%83%A9%E3%82%A4%E3%82%BB%E3%83%B3%E3%82%B9)](LICENSE)

[**⬇ ダウンロード**](https://github.com/rurutheGeek/aihist/releases/latest) · [導入](#導入) · [使い方の詳細](docs/usage.md) · [English](README.en.md)

<img src="docs/images/conversation.png" alt="会話の画面" width="860">

</div>

## できること

- **集める** — 各マシンに小さなプログラムを入れると、AI の会話を自動で送る。ChatGPT・claude.ai・Gemini は書き出したファイルを画面から上げる。
- **探す** — 全部の会話をまとめて全文検索。AI・マシン・プロジェクト・期間・タグで絞れる。
- **続ける** — 会話を元の AI へ戻して続きから話す（`aihist restore`）。別の AI へ渡す Markdown にも書き出せる。
- **まとめる** — 好きな AI の API か MCP で、タグ・要約と、他の人にも役立つ「ナレッジ」の記事を作る。
- **守る** — トークンやパスワードは送る前にマシンで消す。手元の古い会話は、サーバーに届いたものから消せる。
- **自分のもの** — データは自分のサーバーの SQLite と原本のファイルだけ。外のサービスへは送らない。

| ツール | 集め方 |
|---|---|
| Claude Code | 自動（サブエージェントも） |
| Codex | 自動 |
| OpenCode | 自動 |
| Antigravity | 自動 |
| ChatGPT・claude.ai | 書き出しの zip を上げる |
| Gemini | Google Takeout の zip を上げる |

<table>
<tr>
<td><img src="docs/images/knowledge.png" alt="ナレッジの記事"></td>
<td><img src="docs/images/graph.png" alt="ナレッジのグラフ"></td>
</tr>
<tr>
<td><img src="docs/images/search.png" alt="検索"></td>
<td><img src="docs/images/settings.png" alt="マシンの設定"></td>
</tr>
</table>

## 導入

### Docker（おすすめ）

```bash
curl -fsSLO https://raw.githubusercontent.com/rurutheGeek/aihist/main/compose.yaml
docker compose up -d
```

http://localhost:8080 を開く。イメージは `ghcr.io/ruruthegeek/aihist`（linux/amd64・arm64）、データは `/data` に入る。

### 実行ファイル

[リリース](https://github.com/rurutheGeek/aihist/releases/latest)から自分の OS のものを落とす（Linux・macOS・Windows）。

```bash
chmod +x aihist-linux-amd64
./aihist-linux-amd64 serve -addr :8080
```

### ソースから

```bash
git clone https://github.com/rurutheGeek/aihist && cd aihist
go build -o bin/aihist ./cmd/aihist
```

### 会話を送るマシンをつなぐ

画面の「設定 → マシン」でマシン名を入れると、そのマシンで打つ1行のコマンドが出る。あとは決めた時刻に自動で送る。詳しくは[使い方の詳細](docs/usage.md)。

## 更新

新しい版が出ると、画面の左下に知らせが出る（「設定 → 一般」で止められる）。

| 入れ方 | 更新のしかた |
|---|---|
| Docker | `docker compose pull && docker compose up -d` |
| 実行ファイル | `aihist update` のあと起動し直す（SHA256SUMS で確かめてから入れ替える） |
| 各マシンの送るプログラム | サーバーを新しくすれば、次に送るときに自分で入れ替わる |

## ログイン（任意）

何も設定しなければログインなしで動く（自分の PC だけで使うとき）。外から使うときは OIDC（Google・Microsoft Entra ID・Keycloak・Authentik など）でログインさせられる。

| 環境変数 | 意味 |
|---|---|
| `AIHIST_OIDC_ISSUER` | 発行元の URL。入れるとログインが要るようになる |
| `AIHIST_OIDC_CLIENT_ID`・`AIHIST_OIDC_CLIENT_SECRET` | IdP に登録したクライアント |
| `AIHIST_OIDC_REDIRECT_URL` | 戻り先。`https://<サーバー>/auth/callback` を IdP にも登録する |
| `AIHIST_OIDC_ALLOW` | ログインできるメールアドレスか `@ドメイン`（カンマ区切り）。空なら誰でも |
| `AIHIST_OIDC_GROUPS` | ログインできるグループ（`groups` クレーム） |

各マシンからの送信と MCP（`/api/*`）は、ログインではなくトークンで守る。画面は外のサイトの画像やスクリプトを読まず、ほかのサイトに埋め込ませない。

## コントリビュート

Issue・プルリクエストを歓迎します。バグの報告、対応する AI ツールの追加、翻訳、ドキュメントの直しなど、どれでも助かります。始め方は [CONTRIBUTING.md](CONTRIBUTING.md) へ。

## ライセンス

[MIT](LICENSE)
