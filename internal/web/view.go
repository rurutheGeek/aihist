package web

import (
	"html/template"
	"path"
	"strings"
	"time"

	"github.com/rurutheGeek/aihist/internal/model"
)

// Turn は画面の1つの発言。続く assistant とツールの出力は1つにまとめる。
type Turn struct {
	Role      string // user / assistant / system
	At        time.Time
	Model     string
	Sidechain bool
	Blocks    []Block
	Anchor    string
}

type Block struct {
	Kind    string // text / thinking / tool / file / steps（続く思考とツールをまとめたもの）
	Text    string
	Name    string
	Input   string
	Output  string
	IsError bool
	Done    bool // 結果がある
	Steps   []Block
	Tools   int
}

// fold は本文の間に続く思考とツールを1つの「steps」に畳む。1つだけならそのまま。
func fold(bs []Block) []Block {
	var out, run []Block
	flush := func() {
		switch {
		case len(run) == 1:
			out = append(out, run[0])
		case len(run) > 1:
			n := 0
			for _, b := range run {
				if b.Kind == "tool" {
					n++
				}
			}
			out = append(out, Block{Kind: "steps", Steps: run, Tools: n})
		}
		run = nil
	}
	for _, b := range bs {
		if b.Kind == "thinking" || b.Kind == "tool" {
			run = append(run, b)
			continue
		}
		flush()
		out = append(out, b)
	}
	flush()
	return out
}

// turns はメッセージを画面の発言の並びにする。ツールの結果は呼び出しの横に置く。
func turns(msgs []model.Message) []Turn {
	results := map[string]model.Part{}
	calls := map[string]bool{}
	for _, m := range msgs {
		for _, p := range m.Parts {
			switch {
			case p.ToolCallID == "":
			case p.Kind == model.ToolResult:
				results[p.ToolCallID] = p
			case p.Kind == model.ToolCall:
				calls[p.ToolCallID] = true
			}
		}
	}
	var out []Turn
	cur := func(role string, m model.Message) *Turn {
		if role != "user" && len(out) > 0 {
			last := &out[len(out)-1]
			if last.Role == "assistant" && role == "assistant" && last.Sidechain == m.Sidechain {
				if last.Model == "" {
					last.Model = m.Model
				}
				return last
			}
		}
		out = append(out, Turn{Role: role, At: m.CreatedAt, Model: m.Model, Sidechain: m.Sidechain})
		return &out[len(out)-1]
	}
	for _, m := range msgs {
		role := m.Role
		if role == "tool" {
			role = "assistant"
		}
		var blocks []Block
		for _, p := range m.Parts {
			switch p.Kind {
			case model.Text:
				blocks = append(blocks, Block{Kind: "text", Text: p.Text})
			case model.Thinking:
				blocks = append(blocks, Block{Kind: "thinking", Text: p.Text})
			case model.ToolCall:
				b := Block{Kind: "tool", Name: p.ToolName, Input: p.Text}
				if r, ok := results[p.ToolCallID]; ok && p.ToolCallID != "" {
					b.Output, b.IsError, b.Done = r.Text, r.IsError, true
				}
				blocks = append(blocks, b)
			case model.ToolResult:
				if calls[p.ToolCallID] {
					continue
				}
				blocks = append(blocks, Block{Kind: "tool", Name: p.ToolName, Output: p.Text, IsError: p.IsError, Done: true})
			default:
				blocks = append(blocks, Block{Kind: "file", Text: p.Text})
			}
		}
		if len(blocks) == 0 {
			continue
		}
		t := cur(role, m)
		t.Blocks = append(t.Blocks, blocks...)
	}
	for i := range out {
		out[i].Blocks = fold(out[i].Blocks)
		if out[i].Role == "user" {
			out[i].Anchor = "u" + itoa(i)
		}
	}
	return out
}

func itoa(i int) string {
	const d = "0123456789"
	if i == 0 {
		return "0"
	}
	var b []byte
	for ; i > 0; i /= 10 {
		b = append([]byte{d[i%10]}, b...)
	}
	return string(b)
}

var tools = map[string]struct{ Label, Color, Mark string }{
	"claude_code": {"Claude Code", "#d97757", "C"},
	"codex":       {"Codex", "#10a37f", "X"},
	"opencode":    {"OpenCode", "#6d63f2", "O"},
	"antigravity": {"Antigravity", "#3b82f6", "A"},
}

func toolLabel(t string) string {
	if v, ok := tools[t]; ok {
		return v.Label
	}
	return t
}

func toolColor(t string) template.CSS {
	if v, ok := tools[t]; ok {
		return template.CSS(v.Color)
	}
	return "#888"
}

func toolMark(t string) string {
	if v, ok := tools[t]; ok {
		return v.Mark
	}
	return "?"
}

// hl は検索の抜粋（一致を \x02 \x03 で囲む）を、一致を <mark> にした HTML にする。
func hl(s string) template.HTML {
	s = template.HTMLEscapeString(strings.Join(strings.Fields(s), " "))
	s = strings.NewReplacer("\x02", "<mark>", "\x03", "</mark>").Replace(s)
	return template.HTML(s)
}

// day は一覧の見出し（今日・昨日・今週・月）。
func day(t time.Time) string {
	if t.IsZero() {
		return "日付なし"
	}
	now := time.Now()
	y, m, d := now.Date()
	today := time.Date(y, m, d, 0, 0, 0, 0, now.Location())
	switch {
	case !t.Before(today):
		return "今日"
	case !t.Before(today.AddDate(0, 0, -1)):
		return "昨日"
	case !t.Before(today.AddDate(0, 0, -7)):
		return "過去7日"
	case !t.Before(today.AddDate(0, 0, -30)):
		return "過去30日"
	case t.Year() == now.Year():
		return t.Format("1月")
	}
	return t.Format("2006年1月")
}

// summary はツールの入力を1行に縮める（コマンド・パス・パターンなどを優先）。
func summary(in string) string {
	in = strings.TrimSpace(in)
	for _, key := range []string{`"command":"`, `"cmd":"`, `"file_path":"`, `"filePath":"`, `"path":"`, `"pattern":"`, `"url":"`, `"query":"`, `"description":"`} {
		if i := strings.Index(in, key); i >= 0 {
			rest := in[i+len(key):]
			var b strings.Builder
			esc := false
			for _, r := range rest {
				if esc {
					if r == 'n' {
						r = ' '
					}
					b.WriteRune(r)
					esc = false
					continue
				}
				if r == '\\' {
					esc = true
					continue
				}
				if r == '"' {
					break
				}
				b.WriteRune(r)
			}
			in = b.String()
			break
		}
	}
	in = strings.Join(strings.Fields(in), " ")
	if r := []rune(in); len(r) > 120 {
		in = string(r[:120]) + "…"
	}
	return in
}

func base(p string) string {
	if p == "" {
		return ""
	}
	return path.Base(strings.ReplaceAll(p, `\`, "/"))
}
