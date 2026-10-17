package gemini

import (
	"strings"
	"testing"
)

const sample = `<body><div class="mdl-grid"><div class="outer-cell mdl-cell mdl-cell--12-col mdl-shadow--2dp"><div class="mdl-grid"><div class="header-cell mdl-cell mdl-cell--12-col"><p class="mdl-typography--title">Gemini アプリ<br></p></div><div class="content-cell mdl-cell mdl-cell--6-col mdl-typography--body-1">送信したメッセージ:&nbsp;二つ目の質問 &amp; 続き<br>2026/10/16 21:45:18 JST<br><p>答え2</p>
<br></div><div class="content-cell mdl-cell mdl-cell--6-col mdl-typography--body-1 mdl-typography--text-right"></div><div class="content-cell mdl-cell mdl-cell--12-col mdl-typography--caption"><b>サービス:</b><br>&emsp;Gemini アプリ<br><b>詳細:</b><br>&emsp;https://gemini.google.com/app/214cdbd30348620d: <a href="https://gemini.google.com/app/214cdbd30348620d">x</a></div></div></div>` +
	`<div class="outer-cell mdl-cell mdl-cell--12-col mdl-shadow--2dp"><div class="mdl-grid"><div class="content-cell mdl-cell mdl-cell--6-col mdl-typography--body-1">送信したメッセージ: 一つ目
二行目<br>2026/10/16 21:45:04 JST<br><p>答え1</p><ul><li>a</li></ul>
<br></div><div class="content-cell mdl-cell mdl-cell--6-col mdl-typography--body-1 mdl-typography--text-right"></div><div class="content-cell mdl-cell mdl-cell--12-col mdl-typography--caption">https://gemini.google.com/app/214cdbd30348620d</div></div></div>` +
	`<div class="outer-cell mdl-cell"><div class="content-cell mdl-cell mdl-cell--6-col mdl-typography--body-1">Prompted hello<br>Oct 16, 2026, 5:27:32 PM JST<br><p>hi</p><br></div><div class="content-cell mdl-cell mdl-cell--6-col mdl-typography--body-1 mdl-typography--text-right"></div>https://gemini.google.com/app/d7dcaf1e42363611</div>`

func TestParse(t *testing.T) {
	if !Is([]byte(sample)) {
		t.Fatal("Is")
	}
	es := Entries(sample)
	if len(es) != 3 {
		t.Fatalf("entries %d", len(es))
	}
	if es[0].Prompt != "二つ目の質問 & 続き" || es[1].Prompt != "一つ目\n二行目" || es[2].Prompt != "hello" {
		t.Fatalf("%q %q %q", es[0].Prompt, es[1].Prompt, es[2].Prompt)
	}
	if es[0].At.UTC().Format("15:04:05") != "12:45:18" || es[2].At.UTC().Format("15:04") != "08:27" {
		t.Fatalf("time %v %v", es[0].At.UTC(), es[2].At.UTC())
	}
	cs := Group(append(es, es[0]))
	if len(cs) != 2 || len(cs[0].Entries) != 2 {
		t.Fatalf("group %+v", cs)
	}
	m := Parse(cs[0])
	if m.Title != "一つ目" || len(m.Messages) != 4 || m.Messages[1].Role != "assistant" || !strings.Contains(m.Messages[1].Parts[0].Text, "<li>a</li>") {
		t.Fatalf("%+v", m)
	}
}
