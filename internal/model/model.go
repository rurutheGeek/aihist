// Package model はツールに依存しない会話の内部モデル。
package model

import (
	"regexp"
	"strings"
	"time"
)

type Conversation struct {
	Tool       string // claude_code / opencode / ...
	Host       string
	ExternalID string // ツール側の会話ID
	ParentID   string // サブエージェントなど親会話の ExternalID
	Title      string
	Cwd        string
	GitBranch  string
	Model      string
	Models     []string // メッセージに無いとき、会話で使ったモデル
	StartedAt  time.Time
	UpdatedAt  time.Time
	SourcePath string
	Auto       bool // 人ではなくプログラムや AI が動かした会話（opencode run・claude -p・codex exec など）
	Messages   []Message
}

type Message struct {
	ExternalID string
	ParentID   string // ExternalID で親を指す（木構造）
	Role       string // user / assistant / system / tool
	CreatedAt  time.Time
	Model      string
	Sidechain  bool
	Parts      []Part
}

type PartKind string

const (
	Text       PartKind = "text"
	Thinking   PartKind = "thinking"
	ToolCall   PartKind = "tool_call"
	ToolResult PartKind = "tool_result"
	Image      PartKind = "image"
	File       PartKind = "file"
)

type Part struct {
	Kind       PartKind
	Text       string // 検索・表示用の本文（tool_call は引数 JSON）
	ToolName   string
	ToolCallID string
	IsError    bool
}

var transcript = regexp.MustCompile(`([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})\.jsonl`)

// ContinuesFrom は、文脈があふれて前の会話の要約から始めた会話の最初の本文から、
// 前の会話の ID を拾う（Claude Code は要約に前の会話の記録のパスを書く）。
func ContinuesFrom(first string) string {
	if !strings.HasPrefix(strings.TrimSpace(first), "This session is being continued from a previous conversation") {
		return ""
	}
	if m := transcript.FindStringSubmatch(first); m != nil {
		return m[1]
	}
	return ""
}

// MainLine は巻き戻して捨てた枝を除いたメッセージを返す。同じ親に、あとから別の発言を
// 送り直したとき（Claude Code の巻き戻しなど）、先に送った発言と、それに続く枝を除く。
// 途中で止めて送り直したときの AI の発言（操作はもう済んでいる）は残す。親の ID が無い記録はそのまま。
func MainLine(msgs []Message) []Message {
	idx := map[string]int{}
	for i, m := range msgs {
		if m.ExternalID != "" && !m.Sidechain {
			idx[m.ExternalID] = i
		}
	}
	kids := map[string][]int{}
	for i, m := range msgs {
		if m.ParentID != "" && !m.Sidechain {
			if _, ok := idx[m.ParentID]; ok {
				kids[m.ParentID] = append(kids[m.ParentID], i)
			}
		}
	}
	drop := map[int]bool{}
	var cut func(i int)
	cut = func(i int) {
		if drop[i] {
			return
		}
		drop[i] = true
		for _, k := range kids[msgs[i].ExternalID] {
			cut(k)
		}
	}
	for _, ch := range kids {
		last := -1
		for _, i := range ch {
			if msgs[i].Role == "user" {
				last = i
			}
		}
		for _, i := range ch {
			if i < last && msgs[i].Role == "user" {
				cut(i)
			}
		}
	}
	if len(drop) == 0 {
		return msgs
	}
	out := make([]Message, 0, len(msgs)-len(drop))
	for i, m := range msgs {
		if !drop[i] {
			out = append(out, m)
		}
	}
	return out
}
