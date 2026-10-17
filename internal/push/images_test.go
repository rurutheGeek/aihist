package push

import (
	"io"
	"strings"
	"testing"
)

func TestStripped(t *testing.T) {
	big := strings.Repeat("A", 500)
	in := `{"content":[{"type":"image","source":{"type":"base64","media_type":"image/jpeg","data":"` + big + `"}}]}` + "\n" +
		`{"image_url":"data:image/webp;base64,` + big + `"}` + "\n" + `{"text":"short data:image/png;base64,AAAA"}`
	f, n, err := stripped(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	defer drop(f)
	b, _ := io.ReadAll(f)
	out := string(b)
	if strings.Contains(out, big) || int64(len(b)) != n || !strings.Contains(out, `"media_type":"image/png","data":"`+tinyPNG) ||
		!strings.Contains(out, "data:image/png;base64,"+tinyPNG) || !strings.HasSuffix(out, `AAAA"}`) {
		t.Fatal(out)
	}
}
