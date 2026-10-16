package claudeai

import (
	"testing"

	"github.com/rurutheGeek/aihist/internal/model"
)

const sample = `[{"uuid":"c1","name":"題名","created_at":"2026-10-01T00:00:00Z","updated_at":"2026-10-01T00:01:00Z","chat_messages":[
{"uuid":"m1","sender":"human","text":"こんにちは","content":[{"type":"text","text":"こんにちは"}],"created_at":"2026-10-01T00:00:00Z","attachments":[{"file_name":"a.txt","extracted_content":"中身"}],"files":[]},
{"uuid":"m2","parent_message_uuid":"m1","sender":"assistant","content":[{"type":"thinking","thinking":"考える"},{"type":"tool_use","id":"t1","name":"web_search","input":{"q":"x"}},{"type":"tool_result","name":"web_search","content":[{"type":"text","text":"結果"}]},{"type":"text","text":"どうも"}],"created_at":"2026-10-01T00:00:01Z"},
{"uuid":"m3","sender":"assistant","text":"","content":[],"created_at":"2026-10-01T00:00:02Z"}
]}]`

func TestParse(t *testing.T) {
	if !Is([]byte(sample)) {
		t.Fatal("Is")
	}
	cs, err := Split([]byte(sample))
	if err != nil || len(cs) != 1 || cs[0].ID != "c1" {
		t.Fatalf("split: %v %+v", err, cs)
	}
	c, err := Parse(cs[0].Raw)
	if err != nil {
		t.Fatal(err)
	}
	if c.Title != "題名" || c.Tool != Tool || len(c.Messages) != 2 {
		t.Fatalf("conv: %+v", c)
	}
	u, a := c.Messages[0], c.Messages[1]
	if u.Role != "user" || len(u.Parts) != 2 || u.Parts[1].Kind != model.File {
		t.Fatalf("user: %+v", u)
	}
	kinds := []model.PartKind{model.Thinking, model.ToolCall, model.ToolResult, model.Text}
	if a.Role != "assistant" || a.ParentID != "m1" || len(a.Parts) != 4 {
		t.Fatalf("assistant: %+v", a)
	}
	for i, k := range kinds {
		if a.Parts[i].Kind != k {
			t.Fatalf("part %d: %+v", i, a.Parts[i])
		}
	}
	if a.Parts[2].Text != "結果" || a.Parts[1].Text != `{"q":"x"}` {
		t.Fatalf("tool: %+v", a.Parts)
	}
}
