// Package model はツールに依存しない会話の内部モデル。
package model

import "time"

type Conversation struct {
	Tool       string // claude_code / opencode / ...
	Host       string
	ExternalID string // ツール側の会話ID
	ParentID   string // サブエージェントなど親会話の ExternalID
	Title      string
	Cwd        string
	GitBranch  string
	Model      string
	StartedAt  time.Time
	UpdatedAt  time.Time
	SourcePath string
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
