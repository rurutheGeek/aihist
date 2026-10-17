package ai

import (
	"fmt"
	"strings"

	"github.com/rurutheGeek/aihist/internal/model"
)

// 会話を AI に読ませる形の文字にする。思考は省き、ツールの入出力は短くする。
const (
	toolIn  = 400
	toolOut = 600
	toolErr = 1200
)

// PageSize は1回に読ませる量（文字数）。
const PageSize = 40000

func cut(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n/2]) + " …（中略）… " + string(r[len(r)-n/2:])
}

// lines は会話を1発言ずつの文字にする。
func lines(msgs []model.Message) []string {
	var out []string
	for _, m := range msgs {
		if m.Sidechain {
			continue
		}
		var b strings.Builder
		for _, p := range m.Parts {
			switch p.Kind {
			case model.Text:
				if t := strings.TrimSpace(p.Text); t != "" {
					b.WriteString(t + "\n")
				}
			case model.ToolCall:
				fmt.Fprintf(&b, "[操作 %s] %s\n", p.ToolName, cut(strings.TrimSpace(p.Text), toolIn))
			case model.ToolResult:
				n, label := toolOut, "結果"
				if p.IsError {
					n, label = toolErr, "エラー"
				}
				fmt.Fprintf(&b, "[%s] %s\n", label, cut(strings.TrimSpace(p.Text), n))
			case model.Image, model.File:
				b.WriteString("[添付ファイル]\n")
			}
		}
		if t := strings.TrimSpace(b.String()); t != "" {
			who := "AI"
			if m.Role == "user" {
				who = "ユーザー"
			}
			out = append(out, "## "+who+"\n"+t)
		}
	}
	return out
}

// Pages は会話を、size 文字ほどずつの区切りに分ける（発言の途中では切らない。1発言が長すぎるときだけ切る）。
func Pages(msgs []model.Message, size int) []string {
	var pages []string
	var cur strings.Builder
	for _, l := range lines(msgs) {
		if len([]rune(l)) > size {
			l = cut(l, size)
		}
		if cur.Len() > 0 && len([]rune(cur.String()))+len([]rune(l)) > size {
			pages = append(pages, cur.String())
			cur.Reset()
		}
		cur.WriteString(l + "\n\n")
	}
	if cur.Len() > 0 {
		pages = append(pages, cur.String())
	}
	return pages
}
