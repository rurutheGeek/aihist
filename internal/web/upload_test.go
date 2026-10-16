package web

import (
	"archive/zip"
	"bytes"
	"io"
	"testing"
)

type file struct{ *bytes.Reader }

func (file) Close() error { return nil }

func TestDetectWeb(t *testing.T) {
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	w, _ := z.Create("conversations.json")
	io.WriteString(w, `[{"uuid":"c1","chat_messages":[]}]`)
	z.Close()
	cases := []struct {
		name, body, want string
	}{
		{"data.zip", b.String(), "claude_ai"},
		{"conversations.json", `[{"title":"t","mapping":{}}]`, "chatgpt"},
		{"other.json", `{"a":1}`, ""},
	}
	for _, c := range cases {
		r := bytes.NewReader([]byte(c.body))
		if got := detectWeb(c.name, file{r}, int64(r.Len())); got != c.want {
			t.Errorf("%s: %q", c.name, got)
		}
	}
}
