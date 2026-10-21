package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// Tag は利用者が作ったタグ。説明は AI が当てはめるときの手がかり。
type Tag struct {
	Name string `json:"name"`
	Desc string `json:"description,omitempty"`
}

// Knowledge は苦労して解決した問題を、他の人にも役立つ形にまとめたもの。
type Knowledge struct {
	Title       Text     `json:"title"`       // 検索しやすい「困ったこと」（答えは書かない）
	Conclusion  Text     `json:"conclusion"`  // 何をしたら直ったか
	Environment Text     `json:"environment"` // 関係する機器・ソフト・サービスと版
	Symptom     Text     `json:"symptom"`
	Cause       Text     `json:"cause"`
	Solution    Text     `json:"solution"`
	Failed      Text     `json:"failed"`   // 試したが効かなかったこと
	Struggle    Text     `json:"struggle"` // 何に苦労したか（選ぶときの手がかり。本文には出さない）
	Evidence    []string `json:"evidence"` // つまずきを示す記録の文（そのまま）。記録に無ければ捨てる
	Fixed       []string `json:"fixed"`    // 直ったことを示す記録の文（そのまま）。記録に無ければ捨てる
	Score       int      `json:"score"`    // 残す価値（1〜5）
}

// Text は文字。AI が配列で返したときは箇条書きにして読む。
type Text string

func (t *Text) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		*t = Text(s)
		return nil
	}
	var l []any
	if err := json.Unmarshal(b, &l); err != nil {
		*t = Text(strings.Trim(string(b), `"`))
		return nil
	}
	var out []string
	for _, v := range l {
		out = append(out, "- "+strings.TrimSpace(fmt.Sprint(v)))
	}
	*t = Text(strings.Join(out, "\n"))
	return nil
}

// MinScore より低い記事は捨てる。
const MinScore = 4

// Result は会話1件を整理した結果。
type Result struct {
	Tags      []string    `json:"tags"`
	Summary   string      `json:"summary"`
	Knowledge []Knowledge `json:"knowledge"`
	Profile   []Trait     `json:"profile"`
}

// Trait はユーザーについて会話から分かったこと。
type Trait struct {
	Text     Text   `json:"text"`
	Kind     string `json:"kind"`     // about（どんな人か）/ wish（AI にどう答えてほしいか）
	Source   string `json:"source"`   // self（本人がふつうに話したこと）/ draft / hypothetical / other
	Lasting  bool   `json:"lasting"`  // 長く変わらないことか
	Same     int64  `json:"same"`     // 今のプロフィールの同じ項目の番号
	Replaces int64  `json:"replaces"` // 新しいことで置き換える項目の番号
}

// ProfileReq はプロフィールを書き足すときに渡すもの。nil なら取り出さない。
type ProfileReq struct {
	Existing []ExistingTrait
	Note     string // ユーザーが書いた「指示」
}

type ExistingTrait struct {
	ID   int64
	Kind string
	Text string
}

func langName(lang string) string {
	switch lang {
	case "en":
		return "English"
	case "":
		return "会話で主に使われている言語"
	}
	return "日本語"
}

// LangOf は文の言語（かな・漢字があれば ja、無ければ en）。
func LangOf(s string) string {
	for _, r := range s {
		if unicode.In(r, unicode.Hiragana, unicode.Katakana, unicode.Han) {
			return "ja"
		}
	}
	return "en"
}

// Instructions は整理の決まり（API でも MCP でも同じものを使う）。
func Instructions(tags []Tag, lang string, prof *ProfileReq) string {
	var tl strings.Builder
	for _, t := range tags {
		tl.WriteString("- " + t.Name)
		if t.Desc != "" {
			tl.WriteString("：" + t.Desc)
		}
		tl.WriteString("\n")
	}
	if tl.Len() == 0 {
		tl.WriteString("（タグは無い。tags は空の配列にする）\n")
	}
	keys := `{"tags": [...], "summary": "...", "knowledge": [...]}`
	if prof != nil {
		keys = `{"tags": [...], "summary": "...", "knowledge": [...], "profile": [...]}`
	}
	return `AI との会話の記録を整理する。会話を読み、次の形の JSON だけを返す。

` + keys + `

## tags
次のタグのうち、この会話の主な話題に当てはまるものの名前（1〜3個。迷うものは付けない。無ければ空の配列）。ここに無いタグは作らない。
` + tl.String() + `
## summary
会話の要約。2〜3文。何について話し、どうなったか（結論や結果）。

## knowledge
解決するのに苦労した問題を、あとで同じ問題に当たった人（自分や他の人）の役に立つ短い記事にしたもの。
入れるもの：
- 解決までに試行錯誤があった問題（何度か失敗した、原因がなかなか分からなかった、思い込みが外れた など）で、最後に解決したもの
- 他の環境でも起きうるもの
入れないもの：
- 一度で済んだ作業、手順どおりの実行、質問に答えただけのもの、未解決のもの
- その場だけの打ち間違いや、一回きりの事情
- 研究の成果・作ったものの説明・応募書類の内容（困って解決した話ではないもの）
- 具体的に書けないもの。何が、どういう操作で、どうなったか、何をして直ったかが会話から分からないなら入れない
当てはまるものが無ければ空の配列にする。数を増やそうとしない。ほとんどの会話には無い。1つの会話に別々の問題があれば別々に書く。

各記事の形：
{"title": 検索しやすい「困ったこと」。答えや原因は書かない。60字以内,
 "conclusion": 何をしたら直ったか。設定の名前・値・コマンド・手順など、そのまま真似できる形で。1〜2文,
 "environment": 関係する機器・ソフト・サービスとその版など、誰にでも当てはまる情報だけ。分からなければ空,
 "symptom": 何をしたら、何が起きたか。エラーの文や表示があれば短く引用（無ければ様子を具体的に書く）,
 "cause": 原因,
 "solution": 直し方の手順。会話の中で実際に使ったコマンド・設定・値・操作をそのまま書く（一般論や「〜を見直す」のような書き方はしない）。Markdown の箇条書きやコードブロックを使ってよい,
 "failed": 試したが効かなかったこと。無ければ空,
 "struggle": 何に苦労したか（何度失敗したか、何を思い違えていたか）を記録から1文で,
 "evidence": つまずきを示す記録の中の文を、記録からそのまま写した配列（2つ以上。各60字以内。エラーの文・失敗した結果・ユーザーの「まだ動かない」など）。言い換えない,
 "fixed": 直ったことを示す記録の中の文を、記録からそのまま写した配列（1つ以上。成功した結果の出力・ユーザーの「動いた」など）。言い換えない,
 "score": 残す価値。1〜5 の整数。5 は試行錯誤が多く、他の人もはまりやすい問題。1回で直ったものは 2 以下}

## 守ること
- 記録に無いことを書かない。推測で埋めない。solution には会話の中で実際に効いた方法だけを書き、会話に出ていない別の方法を足さない。cause は会話の中で確かめられた原因だけを書く（確かめていなければ「分かっていない」と書く）。
- 個人の情報を書かない：マシン名・ユーザー名・人名・プロジェクト名・社名・ドメイン・IP アドレス・メールアドレス・パスの中の個人名・トークンやパスワード。必要なら <サーバー>・<プロジェクト>・<ユーザー> のように書く。
- [トークン]・[メール] のような印は、すでに消した情報。そのまま扱う。
- 文章は` + langName(lang) + `で書く。` + profileSection(prof)
}

func profileSection(p *ProfileReq) string {
	if p == nil {
		return ""
	}
	var ex strings.Builder
	for i, t := range p.Existing {
		if i == 300 {
			break
		}
		k := "about"
		if t.Kind == "wish" {
			k = "wish"
		}
		fmt.Fprintf(&ex, "%d. [%s] %s\n", t.ID, k, t.Text)
	}
	if ex.Len() == 0 {
		ex.WriteString("（まだ無い）\n")
	}
	note := ""
	if strings.TrimSpace(p.Note) != "" {
		note = "\n### ユーザーからの指示（必ず従う）\n" + p.Note + "\n"
	}
	return `

## profile
このユーザーがどんな人かを、あとで別の AI が読んで、どんな話題の会話でも答え方を合わせられるよう、短い文で残す。
残すのは、人を紹介するときに言うようなこと：住んでいる地域（市区町村まで）・年代・仕事や学校・立場・経歴・家族・趣味・好きなもの・関心・暮らし・体のこと・性格・ものの考え方。
AI への希望は、どの会話にも通じる答え方の好み（言語・書き方・長さ・報告や質問のしかた）だけ。

残さないもの：
- その会話の作業の中身・進め方・決めたこと（何を作った・直した・調べた、どの手順で進めた）
- 特定のプロジェクトや作業の決まり・方針（設計・配備・命名・UI・使う道具の方針など。これはそのプロジェクトの設定に書くもので、人の紹介ではない）
- 作っている物・使っている道具の細かい説明（「〇〇を開発している」は、趣味や仕事として続いているときに、短く1つだけ）
- AI の答えへの一度きりの文句や注文
迷ったら残さない。1つの会話から残すのは多くても2つ。今のプロフィールにある項目と似ていれば same にする。

まず会話の中から候補を挙げ、候補ごとに次を判断する。
- source：その文の出どころ
  - "self"：ユーザーが自分のこととして、ふつうに話した事実
  - "draft"：応募書類・自己PR・作文・投稿などの下書きとして渡した文や、AI に書かせた文（盛ったり作ったりしていることがある）
  - "hypothetical"：たとえ話・仮定・冗談・皮肉・売り言葉
  - "other"：家族・友人・相手など、ほかの人のこと
- 学年・年齢・勤め先・住まい・役職など、年月がたつと変わることは、会話の日付から「2026年10月時点で大学院の修士1年」のように、いつの話かを入れて書く
- kind：どちらの項目か
  - "about"：その人について（経歴・学年・仕事・住まい・家族・趣味・関心・暮らし・性格・考え方）
  - "wish"：どの会話にも通じる、AI への答え方の好み（言語・書き方・長さ・報告や質問のしかた）
- lasting：その人について続いていること・変わりにくいことか（true）、その会話の用件にだけ関わること・一度きりの出来事か（false）。体のこと・暮らし方なども、続いていることなら true

例：
- 「会社員で、週末は登山をしている」→ source "self", lasting true
- 「志望動機：幼い頃から人の役に立ちたいと…」（応募書類の下書き）→ source "draft"
- 「自分で作った機械かもしれないだろ」→ source "hypothetical"
- 「夫は来月から転職する」→ source "other"
- 「1日1食にしている」→ kind "about", lasting true
- 「英語で返されると読めない。日本語で書いて」→ kind "wish", lasting true
- 「今日はラーメンを3杯食べた」「予算は1万5千円で探して」→ lasting false
- 「PR をマージしてから配備して」「この画面は製品に合わせて」「Terraform で管理している」→ 残さない（作業やプロジェクトの決まり）

各項目：{"text": 1文で（主語はユーザー。家族のことは「ユーザーには〜がいる」のようにユーザーから見て書く）, "kind": "about" か "wish", "source": 上のどれか, "lasting": true か false, "same": 今のプロフィールに同じ内容があればその番号（無ければ 0）, "replaces": 今のプロフィールの項目が古くなった（変わった）ならその番号（無ければ 0）}
推測では書かない。会話からはっきり分かることだけ。無ければ空の配列。

### 今のプロフィール
` + ex.String() + `今のプロフィールと同じこと・言い換えにあたることは、新しい項目にせず same にその番号を入れる。
` + note
}

const readNotes = `AI との会話の記録が長いので、区切りごとに読み進めている。
これまでのメモと次の区切りを読み、メモを書き足して {"notes": "..."} の JSON だけを返す。
メモには、話題の流れ、出てきた問題・試したこと（失敗も）・原因・解決、関係する機器・ソフト・サービスとその版を残す。
あとで要約と、苦労して解決した問題の記事と、ユーザーがどんな人か分かること（本人の話か、下書き・たとえ話・ほかの人の話かも分かるように）を書くのに要ることを落とさない。重複は省き、6000字以内にする。
エラーの文や失敗した結果、ユーザーの「まだ動かない」などの言葉は、あとで引用できるよう記録からそのまま「」で写しておく。
記録に無いことを書かない。`

// Organize は会話（Pages で分けたもの）を読み、タグ・要約・ナレッジを返す。
// 長い会話は区切りごとに別のリクエストで読み進め、それまでのメモを次へ渡す。progress は読んだ区切りの数を受け取る。
func (c Config) Organize(ctx context.Context, title string, pages []string, tags []Tag, lang string, prof *ProfileReq, progress func(done, total int)) (Result, error) {
	if len(pages) == 0 {
		return Result{}, nil
	}
	notes := ""
	for i, p := range pages[:len(pages)-1] {
		user := fmt.Sprintf("# 会話の題名\n%s\n\n# これまでのメモ\n%s\n\n# 区切り %d / %d\n%s", title, or(notes, "（まだ無い）"), i+1, len(pages), p)
		var n struct {
			Notes Text `json:"notes"`
		}
		for try := 0; ; try++ {
			out, err := c.Chat(ctx, readNotes, user, true)
			if err == nil {
				if err = decode(out, &n); err == nil {
					break
				}
			}
			if try == 1 {
				return Result{}, err
			}
		}
		notes = string(n.Notes)
		if progress != nil {
			progress(i+1, len(pages))
		}
	}
	user := "# 会話の題名\n" + title + "\n\n"
	if notes != "" {
		user += "# ここまでのメモ（前の区切りを読んだもの）\n" + notes + "\n\n# 最後の区切り\n"
	} else {
		user += "# 会話\n"
	}
	user += pages[len(pages)-1]
	var r Result
	// 返事の形が崩れていたら、もう1回だけ頼む
	for try := 0; ; try++ {
		out, err := c.Chat(ctx, Instructions(tags, lang, prof), user, true)
		if err == nil {
			if err = decode(out, &r); err == nil {
				break
			}
		}
		if try == 1 {
			return Result{}, err
		}
	}
	if progress != nil {
		progress(len(pages), len(pages))
	}
	r = r.Clean(tags, strings.Join(pages, "\n"))
	if prof == nil {
		r.Profile = nil
	}
	return r, nil
}

// Clean は決まりから外れたもの（無いタグ・空の記事・価値の低い記事・つまずきの証拠が記録に無い記事）を落とす。
func (r Result) Clean(tags []Tag, text string) Result {
	ok := map[string]bool{}
	for _, t := range tags {
		ok[t.Name] = true
	}
	var ts []string
	for _, t := range r.Tags {
		if ok[t] {
			ts = append(ts, t)
		}
	}
	r.Tags = ts
	var ks []Knowledge
	for _, k := range r.Knowledge {
		k.Title = Text(strings.TrimSpace(string(k.Title)))
		if k.Title == "" || strings.TrimSpace(string(k.Conclusion)) == "" || strings.TrimSpace(string(k.Solution)) == "" || k.Score < MinScore || !proven(k.Evidence, text) || !anyQuoted(k.Fixed, text) {
			continue
		}
		ks = append(ks, k)
	}
	r.Knowledge = ks
	r.Summary = strings.TrimSpace(r.Summary)
	// プロフィールは、本人がふつうに話した、長く変わらないことだけ
	var ps []Trait
	for _, p := range r.Profile {
		p.Text = Text(strings.TrimSpace(string(p.Text)))
		if p.Text == "" || p.Source != "self" || !p.Lasting {
			continue
		}
		ps = append(ps, p)
	}
	r.Profile = ps
	return r
}

// proven は証拠の文が2つ以上、記録の中にそのまま（空白の違いは問わず）あるか。
func proven(ev []string, text string) bool {
	t := squash(text)
	n := 0
	for _, e := range ev {
		if quoted(squash(e), t) {
			n++
		}
	}
	return n >= 2
}

// anyQuoted は文のどれか1つが記録の中にそのままあるか。
func anyQuoted(ev []string, text string) bool {
	t := squash(text)
	for _, e := range ev {
		if quoted(squash(e), t) {
			return true
		}
	}
	return false
}

var pieces = regexp.MustCompile(`[、。「」（）()”“…]+|等|など`)

// quoted は e（またはその中の12文字以上のひとかたまり）が t にそのままあるか。
// AI は引用のあとに「〜で探したがゼロ」のような言葉を足すことがあるので、かたまりごとに見る。
func quoted(e, t string) bool {
	e = strings.Trim(e, "「」\"'`…. ")
	if len([]rune(e)) >= 6 && strings.Contains(t, e) {
		return true
	}
	for _, p := range pieces.Split(e, -1) {
		p = strings.Trim(p, "\"'` ")
		if len([]rune(p)) >= 12 && strings.Contains(t, p) {
			return true
		}
	}
	return false
}

// squash は空白と表の区切り（|）を除く。AI は引用するとき空白の数や表の形を変えることがあるので、比べる前にそろえる。
func squash(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || r == '|' || r == '│' {
			return -1
		}
		return r
	}, s)
}

func or(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// Markdown はナレッジの本文（題名を除く）。見出しは画面の言語で。
func (k Knowledge) Markdown(lang string) string {
	h := map[string]string{"c": "結論", "e": "環境", "s": "症状", "a": "原因", "x": "解決方法", "f": "うまくいかなかった方法"}
	if lang == "en" {
		h = map[string]string{"c": "Conclusion", "e": "Environment", "s": "Symptom", "a": "Cause", "x": "Solution", "f": "What didn't work"}
	}
	var b strings.Builder
	add := func(k string, t Text) {
		if v := strings.TrimSpace(string(t)); v != "" {
			b.WriteString("## " + h[k] + "\n\n" + v + "\n\n")
		}
	}
	add("c", k.Conclusion)
	add("e", k.Environment)
	add("s", k.Symptom)
	add("a", k.Cause)
	add("x", k.Solution)
	add("f", k.Failed)
	return strings.TrimSpace(b.String()) + "\n"
}
