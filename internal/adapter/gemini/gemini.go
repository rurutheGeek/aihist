// Package gemini は Google Takeout の「マイ アクティビティ」の Gemini アプリの記録（HTML）を読む。
//
// 1件（outer-cell）が「送った質問・日時・答え（HTML）」で、下に会話の URL
// （https://gemini.google.com/app/<会話 ID>）が付く。会話 ID ごとにまとめ、古い順に並べる。
// 並びは新しい順。見出しや日付の書き方はアカウントの言語で変わる（日本語と英語を読む）。
package gemini

import (
	"crypto/sha256"
	"encoding/hex"
	"html"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/rurutheGeek/aihist/internal/model"
)

const Tool = "gemini"

// parser は読み方の版。上げると取り込み済みの会話も読み直す。
const parser = "v1;"

// Entry は1件の質問と答え。
type Entry struct {
	ConvID string
	At     time.Time
	Prompt string // 文字
	Answer string // HTML
}

var (
	appURL  = regexp.MustCompile(`https://gemini\.google\.com/app/([0-9a-f]+)`)
	bodyEnd = `</div><div class="content-cell mdl-cell mdl-cell--6-col mdl-typography--body-1 mdl-typography--text-right">`
	bodyTag = `mdl-typography--body-1">`
	tagRe   = regexp.MustCompile(`<[^>]+>`)
	// 日時: 2026/10/16 21:45:18 JST / Oct 16, 2026, 9:45:18 PM JST
	jaDate = regexp.MustCompile(`^\d{4}/\d{1,2}/\d{1,2} \d{1,2}:\d{2}:\d{2} [A-Z]{2,5}$`)
	enDate = regexp.MustCompile(`^[A-Z][a-z]{2} \d{1,2}, \d{4}, \d{1,2}:\d{2}:\d{2}\s?[AP]M [A-Z]{2,5}$`)
)

// Is は HTML が Gemini アプリのマイ アクティビティか（先頭の数 MB で見る）。
func Is(head []byte) bool {
	s := string(head)
	return strings.Contains(s, "outer-cell") && appURL.MatchString(s)
}

// Entries は HTML から質問と答えを取り出す。
func Entries(doc string) []Entry {
	var out []Entry
	cells := strings.Split(doc, `<div class="outer-cell`)
	for _, c := range cells[1:] {
		m := appURL.FindStringSubmatch(c)
		if m == nil {
			continue
		}
		i := strings.Index(c, bodyTag)
		if i < 0 {
			continue
		}
		body := c[i+len(bodyTag):]
		if j := strings.Index(body, bodyEnd); j >= 0 {
			body = body[:j]
		}
		// 「質問<br>日時<br>答え」。質問の中にも <br> があり得るので、日時の行を探して分ける
		parts := strings.Split(body, "<br>")
		di := -1
		var at time.Time
		for k, p := range parts {
			if t, ok := parseDate(strings.TrimSpace(p)); ok {
				di, at = k, t
				break
			}
		}
		if di < 0 {
			continue
		}
		prompt := html.UnescapeString(tagRe.ReplaceAllString(strings.Join(parts[:di], "\n"), ""))
		prompt = strings.TrimSpace(stripLabel(prompt))
		answer := strings.TrimSpace(strings.Join(parts[di+1:], "<br>"))
		answer = strings.TrimSuffix(answer, "<br>")
		if prompt == "" && answer == "" {
			continue
		}
		out = append(out, Entry{ConvID: m[1], At: at, Prompt: prompt, Answer: answer})
	}
	return out
}

// stripLabel は先頭の「送信したメッセージ: 」「Prompted 」を外す。
func stripLabel(s string) string {
	// 見出しと本文の間は NBSP のことがある
	if i := strings.Index(s, "\u00a0"); i >= 0 && i <= 40 {
		s = s[:i] + " " + s[i+len("\u00a0"):]
	}
	if t, ok := strings.CutPrefix(s, "Prompted "); ok {
		return t
	}
	if i := strings.Index(s, ": "); i >= 0 && i <= 30 && !strings.ContainsAny(s[:i], "\n") {
		return s[i+2:]
	}
	return s
}

func parseDate(s string) (time.Time, bool) {
	var layout string
	switch {
	case jaDate.MatchString(s):
		layout = "2006/1/2 15:04:05 MST"
	case enDate.MatchString(s):
		layout = "Jan 2, 2006, 3:04:05 PM MST"
		s = strings.Replace(s, " ", " ", 1)
	default:
		return time.Time{}, false
	}
	t, err := time.Parse(layout, s)
	if err != nil {
		return time.Time{}, false
	}
	// 略称の時間帯（JST など）は Go では位置が分からないので、よく出るものは補う
	if z, off := t.Zone(); off == 0 && z != "UTC" && z != "GMT" {
		if o, ok := zones[z]; ok {
			t = t.Add(-time.Duration(o) * time.Second)
		}
	}
	return t, true
}

var zones = map[string]int{"JST": 9 * 3600, "KST": 9 * 3600, "PST": -8 * 3600, "PDT": -7 * 3600, "EST": -5 * 3600, "EDT": -4 * 3600, "CET": 3600, "CEST": 7200, "BST": 3600}

// Conversation は会話 ID ごとの件と、取り込みを省くかの判定に使う印。
type Conversation struct {
	ID      string
	Stamp   string
	Entries []Entry
}

// Group は件を会話ごとにまとめる（同じ件が何度出ても1つにする）。
func Group(es []Entry) []Conversation {
	by := map[string]map[string]Entry{}
	for _, e := range es {
		k := e.At.UTC().Format(time.RFC3339) + "\x00" + e.Prompt
		if by[e.ConvID] == nil {
			by[e.ConvID] = map[string]Entry{}
		}
		by[e.ConvID][k] = e
	}
	var out []Conversation
	for id, m := range by {
		c := Conversation{ID: id}
		for _, e := range m {
			c.Entries = append(c.Entries, e)
		}
		sort.Slice(c.Entries, func(i, j int) bool { return c.Entries[i].At.Before(c.Entries[j].At) })
		h := sha256.New()
		for _, e := range c.Entries {
			h.Write([]byte(e.At.UTC().Format(time.RFC3339) + e.Prompt + e.Answer))
		}
		c.Stamp = parser + hex.EncodeToString(h.Sum(nil))[:16]
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Parse は会話を内部モデルにする。
func Parse(c Conversation) *model.Conversation {
	m := &model.Conversation{Tool: Tool, ExternalID: c.ID, SourcePath: "https://gemini.google.com/app/" + c.ID}
	prev := ""
	for i, e := range c.Entries {
		if i == 0 {
			m.StartedAt = e.At
			m.Title = firstLine(e.Prompt)
		}
		m.UpdatedAt = e.At
		uid := c.ID + "-" + e.At.UTC().Format("20060102150405") + "-u"
		m.Messages = append(m.Messages, model.Message{ExternalID: uid, ParentID: prev, Role: "user", CreatedAt: e.At,
			Parts: []model.Part{{Kind: model.Text, Text: e.Prompt}}})
		prev = uid
		if e.Answer != "" {
			aid := c.ID + "-" + e.At.UTC().Format("20060102150405") + "-a"
			m.Messages = append(m.Messages, model.Message{ExternalID: aid, ParentID: prev, Role: "assistant", CreatedAt: e.At,
				Parts: []model.Part{{Kind: model.Text, Text: e.Answer}}})
			prev = aid
		}
	}
	return m
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if r := []rune(s); len(r) > 80 {
		s = string(r[:80]) + "…"
	}
	return s
}
