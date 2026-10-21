package ai

import (
	"context"
	"fmt"
	"strings"
)

// Trait はユーザーについて会話から分かったこと。
type Trait struct {
	Text     Text   `json:"text"`
	Kind     string `json:"kind"`     // about（どんな人か）/ wish（AI にどう答えてほしいか）
	Source   string `json:"source"`   // self（本人がふつうに話したこと）/ draft / hypothetical / other
	Lasting  bool   `json:"lasting"`  // 長く変わらないことか
	Same     int64  `json:"same"`     // 今のプロフィールの同じ項目の番号
	Replaces int64  `json:"replaces"` // 新しいことで置き換える項目の番号
	Quote    string `json:"quote"`    // 根拠になる本人の発言（そのまま）。発言に無ければ捨てる
}

// ProfileReq はプロフィールを書き足すときに渡すもの。
type ProfileReq struct {
	Existing []ExistingTrait
	Note     string // ユーザーが書いた「指示」
}

type ExistingTrait struct {
	ID   int64
	Kind string
	Text string
}

// プロフィールに渡す本人の発言の長さ。長い発言は貼り付けとみなし、頭と終わりだけ残す。
const (
	saidMax   = 30000
	pasteOver = 800
	pasteKeep = 200
)

// UserSays は会話（Pages で分けたもの）から、本人が書いた発言だけを取り出す。
// AI の発言・操作の結果・システムが差し込んだ文は除き、長い貼り付けは省く。
func UserSays(pages []string) string {
	var out []string
	for _, blk := range strings.Split("\n\n"+strings.Join(pages, ""), "\n\n## ") {
		t, ok := strings.CutPrefix(blk, "ユーザー\n")
		if !ok {
			continue
		}
		t = strings.TrimSpace(t)
		if t == "" || strings.HasPrefix(t, "[結果]") || strings.HasPrefix(t, "[エラー]") || strings.HasPrefix(t, "[操作 ") ||
			strings.HasPrefix(t, "<") || strings.HasPrefix(t, skillMarkAI) || strings.HasPrefix(t, "Caveat:") {
			continue
		}
		if r := []rune(t); len(r) > pasteOver {
			t = string(r[:pasteKeep]) + fmt.Sprintf("\n［貼り付け %d 字を省いた］\n", len(r)-2*pasteKeep) + string(r[len(r)-pasteKeep:])
		}
		out = append(out, t)
	}
	return cut(strings.Join(out, "\n---\n"), saidMax)
}

// skillMarkAI は Claude Code が Skill を読み込んだときの本文の頭（本人の発言ではない）。
const skillMarkAI = "Base directory for this skill: "

// ProfileInstructions はプロフィールを書き足す決まり（API でも MCP でも同じものを使う）。
func ProfileInstructions(p *ProfileReq, lang string) string {
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
		note = "\n## ユーザーからの指示（必ず従う）\n" + p.Note + "\n"
	}
	return `ある人が AI に送った発言だけを並べたものを読み、この人のプロフィールに足すことを探す。
プロフィールは、別の AI が料理・旅行・体調・仕事など、どんな話題でもこの人に合った答え方をするために読む。
{"profile": [...]} の JSON だけを返す。

## 残すもの
- about：友人がこの人を紹介するときに話すようなこと。年代・住む地域・家族・仕事や学校・経歴・趣味・好きなもの・暮らし方・体のこと・性格・考え方
- wish：この人が「こう答えてほしい」とはっきり頼んだ、どの話題でも通じる話し方（言語・長さ・口調）

## 残さないもの
- 用件・作業の中身・作業の決まりや方針・道具や技術の話・AI の作業のしかたへの注文
- 名前・URL・IP アドレス
- 一度きりのこと（今日の気分・その場の予算・その場の文句）
- 貼り付けた文（メール・資料・記事・下書き）の中身。あいさつと名乗り（「〇〇課の△△です」）で始まる文は、その人から届いた手紙で、この人が書いたものではない。この人はそれを受け取った側
- 推し量ったこと。本人が自分について言っていないことは書かない（使っている道具から職業を決めない）
- その会話の AI への返し方の注文（「敬語やめて」「短く」）。どの会話でも同じように頼んでいると分かるものだけ wish にする

ほとんどの発言からは何も残らない。迷ったら残さない。多くても2つ。

## 各項目
{"quote": 根拠になる本人の発言を、発言からそのまま写したもの（60字以内。言い換えない）, "text": 主語「ユーザーは」の40字ほどの1文, "kind": "about" か "wish", "source": "self"（本人の事実）/"draft"（書類の下書き）/"hypothetical"（たとえ・冗談）/"other"（ほかの人のこと）, "lasting": 続いていることなら true, "same": 今のプロフィールに同じことがあればその番号（無ければ 0）, "replaces": 今の項目が変わったならその番号（無ければ 0）}
学年・年齢・勤め先・住まいなど年月で変わることは「2026年10月時点で」のように、会話の日付からいつの話かを入れる。
文章は` + langName(lang) + `で書く。

## 今のプロフィール
` + ex.String() + note
}

// Profile は本人の発言 said を読み、プロフィールに足すことを返す。
func (c Config) Profile(ctx context.Context, title, said, lang string, p *ProfileReq) ([]Trait, error) {
	if strings.TrimSpace(said) == "" {
		return nil, nil
	}
	user := "# 会話の題名\n" + title + "\n\n# この人の発言\n" + said
	var r struct {
		Profile []Trait `json:"profile"`
	}
	for try := 0; ; try++ {
		out, err := c.Chat(ctx, ProfileInstructions(p, lang), user, true)
		if err == nil {
			if err = decode(out, &r); err == nil {
				break
			}
		}
		if try == 1 {
			return nil, err
		}
	}
	return CleanTraits(r.Profile, said), nil
}

// CleanTraits は本人がふつうに話した、長く変わらないことで、根拠が本人の発言 said にそのままあるものだけ残す（多くても2つ）。
func CleanTraits(ts []Trait, said string) []Trait {
	var out []Trait
	sq := squash(said)
	for _, t := range ts {
		t.Text = Text(strings.TrimSpace(string(t.Text)))
		q := squash(strings.Trim(strings.TrimSpace(t.Quote), "「」\"'"))
		if t.Text == "" || t.Source != "self" || !t.Lasting || len([]rune(q)) < 4 || !strings.Contains(sq, q) {
			continue
		}
		if out = append(out, t); len(out) == 2 {
			break
		}
	}
	return out
}
