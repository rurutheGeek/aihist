package chatgpt

import (
	"testing"

	"github.com/rurutheGeek/aihist/internal/model"
)

const sample = `[{"title":"題名","create_time":1759276800.5,"update_time":1759276900,"conversation_id":"c1","current_node":"a2","default_model_slug":"gpt-5",
"mapping":{
"root":{"id":"root","message":null,"parent":null,"children":["s"]},
"s":{"id":"s","message":{"id":"s","author":{"role":"system"},"content":{"content_type":"text","parts":[""]},"metadata":{"is_visually_hidden_from_conversation":true}},"parent":"root","children":["u1"]},
"u1":{"id":"u1","message":{"id":"u1","author":{"role":"user"},"create_time":1759276801,"content":{"content_type":"multimodal_text","parts":[{"content_type":"image_asset_pointer","asset_pointer":"file-service://x"},"こんにちは"]},"metadata":{}},"parent":"s","children":["t","a2"]},
"t":{"id":"t","message":{"id":"t","author":{"role":"assistant"},"content":{"content_type":"thoughts","thoughts":[{"summary":"考える","content":"中身"}]},"metadata":{"model_slug":"o3"}},"parent":"u1","children":["a1"]},
"a1":{"id":"a1","message":{"id":"a1","author":{"role":"assistant"},"content":{"content_type":"text","parts":["一つ目"]},"metadata":{"model_slug":"o3"}},"parent":"t","children":[]},
"a2":{"id":"a2","message":{"id":"a2","author":{"role":"assistant"},"content":{"content_type":"text","parts":["二つ目"]},"metadata":{"model_slug":"gpt-5"}},"parent":"u1","children":[]}
}}]`

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
	if c.Title != "題名" || c.ExternalID != "c1" || c.StartedAt.Unix() != 1759276800 {
		t.Fatalf("meta: %+v", c)
	}
	// 隠れた system は飛ばし、分岐は両方残す
	if len(c.Messages) != 4 {
		t.Fatalf("messages: %+v", c.Messages)
	}
	u := c.Messages[0]
	if u.Role != "user" || u.ParentID != "" || u.Parts[0].Kind != model.Image || u.Parts[1].Text != "こんにちは" {
		t.Fatalf("user: %+v", u)
	}
	th, a1, a2 := c.Messages[1], c.Messages[2], c.Messages[3]
	if th.Parts[0].Kind != model.Thinking || th.ParentID != "u1" || a1.ParentID != "t" || a2.ParentID != "u1" || a2.Model != "gpt-5" {
		t.Fatalf("tree: %+v %+v %+v", th, a1, a2)
	}
}
