// Package export は会話を別の AI に渡せる形へ書き出す。
package export

import (
	"fmt"
	"io"
	"strings"

	"github.com/rurutheGeek/aihist/internal/model"
	"github.com/rurutheGeek/aihist/internal/store"
)

type Options struct {
	Thinking       bool // 思考を含める
	Tools          bool // ツールの呼び出しと結果を含める
	MaxToolOutput  int  // ツール結果を先頭から何文字で切るか（0 は切らない）
	SkipSidechains bool
}

var Default = Options{Tools: true, MaxToolOutput: 2000, SkipSidechains: true}

func Markdown(w io.Writer, c *store.ConvRow, msgs []model.Message, o Options) {
	fmt.Fprintf(w, "# %s\n\n", oneLine(c.Title))
	fmt.Fprintf(w, "- tool: %s\n- session: %s\n", c.Tool, c.ExternalID)
	if c.Cwd != "" {
		fmt.Fprintf(w, "- cwd: %s\n", c.Cwd)
	}
	if c.GitBranch != "" {
		fmt.Fprintf(w, "- branch: %s\n", c.GitBranch)
	}
	if c.Model != "" {
		fmt.Fprintf(w, "- model: %s\n", c.Model)
	}
	if !c.StartedAt.IsZero() {
		fmt.Fprintf(w, "- period: %s – %s\n", c.StartedAt.Format("2006-01-02 15:04"), c.UpdatedAt.Format("2006-01-02 15:04"))
	}
	for _, m := range msgs {
		if o.SkipSidechains && m.Sidechain {
			continue
		}
		var body strings.Builder
		for _, p := range m.Parts {
			switch p.Kind {
			case model.Text:
				body.WriteString(p.Text + "\n\n")
			case model.Thinking:
				if o.Thinking {
					body.WriteString("<thinking>\n" + p.Text + "\n</thinking>\n\n")
				}
			case model.ToolCall:
				if o.Tools {
					fmt.Fprintf(&body, "**%s** `%s`\n\n", p.ToolName, oneLine(truncate(p.Text, 500)))
				}
			case model.ToolResult:
				if o.Tools {
					label := "result"
					if p.IsError {
						label = "error"
					}
					fmt.Fprintf(&body, "<details><summary>%s</summary>\n\n%s\n\n</details>\n\n", label, fence(truncate(p.Text, o.MaxToolOutput)))
				}
			case model.Image, model.File:
				body.WriteString(p.Text + "\n\n")
			}
		}
		if strings.TrimSpace(body.String()) == "" {
			continue
		}
		fmt.Fprintf(w, "\n## %s\n\n%s", m.Role, body.String())
	}
}

func truncate(s string, n int) string {
	r := []rune(s)
	if n <= 0 || len(r) <= n {
		return s
	}
	return string(r[:n]) + fmt.Sprintf("\n…（残り %d 文字を省略）", len(r)-n)
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// fence は本文中のバッククォートの連続より長い囲みを使う。
func fence(s string) string {
	n := 3
	for run, i := 0, 0; i < len(s); i++ {
		if s[i] == '`' {
			run++
			if run >= n {
				n = run + 1
			}
		} else {
			run = 0
		}
	}
	f := strings.Repeat("`", n)
	return f + "\n" + s + "\n" + f
}
