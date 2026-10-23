# Contributing / コントリビュート

Thanks for helping! Issues and pull requests are welcome in English or Japanese.
日本語でも英語でも歓迎します。

## Reporting bugs / バグの報告

Open an [issue](https://github.com/rurutheGeek/aihist/issues) with the aihist version (bottom of **Settings → General**), your OS, the AI tool involved, and what you expected. Please remove tokens and personal data from logs and chat excerpts.

aihist の版（「設定 → 一般」）、OS、関わる AI ツール、期待した動きを書いてください。ログや会話の抜き出しからトークンや個人情報は消してください。

Security issues: please use [GitHub's private vulnerability reporting](https://github.com/rurutheGeek/aihist/security/advisories/new) instead of a public issue.
セキュリティの問題は公開の Issue ではなく、上のリンクから非公開で知らせてください。

## Development / 開発

Requires Go (see `go.mod`). No C compiler is needed.

```bash
go build -o bin/aihist ./cmd/aihist
bin/aihist -db /tmp/dev.db serve -addr 127.0.0.1:8080
go vet ./... && go test ./...
gofmt -l .
```

- `internal/adapter/<tool>/` reads one AI tool's history. Adding a tool usually means a new adapter, a test with a small sample, and a line in the support table of both READMEs.
- UI text is written in Japanese in the templates and translated in `internal/web/i18n.go`. Add the English for every new string.
- Keep dependencies few; open an issue before adding one.
- One topic per pull request. Describe what changed and how you checked it.

- AI ツールを足すときは `internal/adapter/<ツール>/` にアダプタと小さな見本のテストを置き、両方の README の表に1行足す。
- 画面の文は日本語でテンプレートに書き、`internal/web/i18n.go` に英語を足す。
- 依存はなるべく増やさない。足すときは先に Issue で相談する。
- プルリクエストは1つの話題ごとに。何を変えたか、どう確かめたかを書く。

## License / ライセンス

By contributing, you agree that your contributions are licensed under the [MIT License](LICENSE).
