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
