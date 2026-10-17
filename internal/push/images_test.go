package push

import (
	"io"
	"strings"
	"testing"
)

func TestStripped(t *testing.T) {
	big := strings.Repeat("A", 500)
	in := `{"content":[{"type":"image","source":{"type":"base64","media_type":"image/jpeg","data":"` + big + `"}}]}` + "\n" +
		`{"image_url":"data:image/webp;base64,` + big + `"}` + "\n" +
		`{"source":{"type":"base64","media_type":"application/pdf","data":"` + big + `"}}` + "\n" +
		`{"url":"data:audio/mpeg;base64,` + big + `"}` + "\n" + `{"text":"short data:image/png;base64,AAAA"}`
	f, n, err := stripped(strings.NewReader(in), "-", "")
	if err != nil {
		t.Fatal(err)
	}
	defer drop(f)
	b, _ := io.ReadAll(f)
	out := string(b)
	if strings.Contains(out, big) || int64(len(b)) != n || !strings.Contains(out, `"media_type":"image/png","data":"`+tinyPNG) ||
		!strings.Contains(out, "data:image/png;base64,"+tinyPNG) ||
		!strings.Contains(out, `"media_type":"application/pdf","data":"`+tinyPDF) || !strings.Contains(out, "data:text/plain;base64,"+tinyText) || !strings.HasSuffix(out, `AAAA"}`) {
		t.Fatal(out)
	}
}

func TestSelected(t *testing.T) {
	for _, c := range []struct {
		list, tool string
		want       bool
	}{{"", "codex", true}, {"*", "codex", true}, {"*,-codex", "codex", false}, {"*,-codex", "opencode", true},
		{"codex", "codex", true}, {"codex", "opencode", false}, {"-", "codex", false}} {
		if Selected(c.list, c.tool) != c.want {
			t.Errorf("%+v", c)
		}
	}
}

func TestKeepKinds(t *testing.T) {
	big := strings.Repeat("A", 500)
	in := `{"a":"data:image/png;base64,` + big + `","b":"data:audio/mpeg;base64,` + big + `"}`
	out := string(stripImages([]byte(in), "*,-audio"))
	if !strings.Contains(out, "data:image/png;base64,"+big) || strings.Contains(out, "audio/mpeg;base64,"+big) {
		t.Fatal(out)
	}
	if Kind("application/vnd.openxmlformats-officedocument.wordprocessingml.document") != "doc" || Kind("application/zip") != "other" {
		t.Fatal("kind")
	}
	if !KeepsAll("*") || !KeepsAll("") || KeepsAll("*,-pdf") || KeepsAll("-") {
		t.Fatal("keepsall")
	}
}
