// Package scrub は会話の記録から、秘密の情報と個人情報を決まった規則で見つけて印に置き換える。
// 記録は JSON の行なので、置き換えた後も JSON として壊れないよう、引用符と \ をまたがない形だけを探す。
// 添付ファイルの base64 を壊さないよう、英数字に続く所からは始めない。
package scrub

import (
	"net"
	"regexp"
	"slices"
	"strings"
)

// 消す種類（設定の画面に並べる順）。
const (
	Secret  = "secret"  // トークン・API キー・パスワード・秘密鍵・接続文字列のパスワード
	Email   = "email"   // メールアドレス
	Phone   = "phone"   // 電話番号
	Address = "address" // 住所
	Card    = "card"    // クレジットカード番号
	IP      = "ip"      // IP アドレス
)

var Kinds = []string{Secret, Email, Phone, Address, Card, IP}

// Default は最初に消す種類。
const Default = Secret

// Mark は消した所に入れる印。
var Mark = map[string]string{
	Secret: "[トークン]", Email: "[メール]", Phone: "[電話番号]", Address: "[住所]", Card: "[カード番号]", IP: "[IPアドレス]",
}

// On は並び（"secret,email"・"-" は何も消さない）が kind を消すか。
func On(list, kind string) bool {
	return slices.Contains(strings.Split(strings.ReplaceAll(list, " ", ""), ","), kind)
}

// Any は並びが何か1つでも消すか。
func Any(list string) bool {
	for _, k := range Kinds {
		if On(list, k) {
			return true
		}
	}
	return false
}

// 値の文字（引用符・\・空白・区切りをまたがない）
const val = `[^\s"'\\,;&<>\[\]{}()]`

var (
	// 前が英数字でない所から（base64 や長い ID の途中を拾わない）
	pre = `(^|[^A-Za-z0-9_])`

	secretRes = []*regexp.Regexp{
		regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----[A-Za-z0-9+/=\s\\:,.-]{16,}?-----END [A-Z0-9 ]*PRIVATE KEY-----`),
		regexp.MustCompile(pre + `(sk-(?:proj-|ant-[a-z0-9]+-|live_|test_)?[A-Za-z0-9_-]{20,})`),
		regexp.MustCompile(pre + `((?:gh[pousr]_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{40,}|glpat-[A-Za-z0-9_-]{20,}))`),
		regexp.MustCompile(pre + `((?:AKIA|ASIA)[0-9A-Z]{16})`),
		regexp.MustCompile(pre + `(AIza[0-9A-Za-z_-]{35})`),
		regexp.MustCompile(pre + `(xox[abposr]-[A-Za-z0-9-]{10,})`),
		regexp.MustCompile(pre + `(eyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,})`),
		regexp.MustCompile(`((?i:bearer)\s+)([A-Za-z0-9._~+/-]{20,}=*)`),
	}
	// password=…・"api_key": "…" など（名前と区切りは残して値だけ消す）
	assign = regexp.MustCompile(`(?i)(^|[^A-Za-z])((?:password|passwd|pwd|pw|passphrase|secret|token|api[_-]?key|apikey|access[_-]?key|private[_-]?key|client[_-]?secret|auth[_-]?token)[A-Za-z0-9_-]*` +
		`(?:\\?["'])?\s*[:=]\s*(?:\\?["'])?)(` + val + `{6,})(\(?)`)
	// postgres://user:pass@host のパスワード
	urlCred = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://[^\s:/@"'\\]+:)([^\s@/"'\\]+)(@)`)

	emailRe = regexp.MustCompile(pre + `([A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,})`)
	phoneRe = regexp.MustCompile(`(^|[^0-9A-Za-z_.:/-])((?:\+\d{1,3}[ -]?)?(?:0\d{1,4}-\d{1,4}-\d{3,4}|\(0\d{1,4}\)\s?\d{1,4}-\d{3,4}|0[5789]0\d{8}|\+\d{1,3}[ -]\d{1,4}[ -]\d{2,4}[ -]\d{3,4}))($|[^0-9A-Za-z_:/-])`)
	addrRe  = regexp.MustCompile(`(?:〒\s?\d{3}-?\d{4}\s?)?(?:北海道|東京都|京都府|大阪府|青森県|岩手県|宮城県|秋田県|山形県|福島県|茨城県|栃木県|群馬県|埼玉県|千葉県|神奈川県|新潟県|富山県|石川県|福井県|山梨県|長野県|岐阜県|静岡県|愛知県|三重県|滋賀県|兵庫県|奈良県|和歌山県|鳥取県|島根県|岡山県|広島県|山口県|徳島県|香川県|愛媛県|高知県|福岡県|佐賀県|長崎県|熊本県|大分県|宮崎県|鹿児島県|沖縄県)` +
		`[^\s"'\\、。,]{1,25}?[0-9０-９]+(?:(?:丁目|番地|番|号|の|-|－|ー)[0-9０-９]+)*(?:丁目|番地|番|号)?`)
	postal = regexp.MustCompile(`〒\s?\d{3}-\d{4}`)
	cardRe = regexp.MustCompile(`(^|[^0-9A-Za-z_.-])((?:\d[ -]?){12,18}\d)($|[^0-9A-Za-z_.-])`)
	ipv4Re = regexp.MustCompile(`(^|[^0-9A-Za-z_.])((?:\d{1,3}\.){3}\d{1,3})($|[^0-9A-Za-z_]|\.[^0-9])`)
	ipv6Re = regexp.MustCompile(`(?i)(^|[^0-9a-z_:.])([0-9a-f]*:[0-9a-f]*:[0-9a-f:]*)($|[^0-9a-z_:])`)
)

// Text は s から list の種類を消す。
func Text(s, list string) string {
	if list == "" || list == "-" {
		return s
	}
	if On(list, Secret) {
		for i, re := range secretRes {
			if i == 0 {
				s = re.ReplaceAllString(s, Mark[Secret])
			} else {
				s = re.ReplaceAllString(s, "${1}"+Mark[Secret])
			}
		}
		s = assign.ReplaceAllStringFunc(s, func(m string) string {
			p := assign.FindStringSubmatch(m)
			if p[4] != "" || placeholderish(p[3]) || !secretish(p[2], p[3]) {
				return m
			}
			return p[1] + p[2] + Mark[Secret]
		})
		s = urlCred.ReplaceAllString(s, "${1}"+Mark[Secret]+"${3}")
	}
	if On(list, Email) {
		s = emailRe.ReplaceAllString(s, "${1}"+Mark[Email])
	}
	if On(list, Card) {
		s = replace3(cardRe, s, func(m string) bool { return luhn(m) }, Mark[Card])
	}
	if On(list, Phone) {
		s = replace3(phoneRe, s, nil, Mark[Phone])
	}
	if On(list, Address) {
		s = addrRe.ReplaceAllString(s, Mark[Address])
		s = postal.ReplaceAllString(s, Mark[Address])
	}
	if On(list, IP) {
		s = replace3(ipv4Re, s, func(m string) bool { return net.ParseIP(m) != nil }, Mark[IP])
		s = replace3(ipv6Re, s, func(m string) bool {
			return len(m) >= 6 && strings.ContainsAny(m, "0123456789") && net.ParseIP(m) != nil
		}, Mark[IP])
	}
	return s
}

// Line は1行（JSON の行）から list の種類を消す。
func Line(b []byte, list string) []byte {
	if !Any(list) {
		return b
	}
	return []byte(Text(string(b), list))
}

// replace3 は (前)(本体)(後) の形の re で、ok が通る本体だけを印に置き換える。前後の文字は残す。
// 隣り合う一致が前後の文字を取り合わないよう、置き換えた所の次から探し直す。
func replace3(re *regexp.Regexp, s string, ok func(string) bool, mark string) string {
	var b strings.Builder
	for {
		loc := re.FindStringSubmatchIndex(s)
		if loc == nil {
			b.WriteString(s)
			return b.String()
		}
		body := s[loc[4]:loc[5]]
		if ok == nil || ok(body) {
			b.WriteString(s[:loc[4]])
			b.WriteString(mark)
		} else {
			b.WriteString(s[:loc[5]])
		}
		s = s[loc[5]:]
	}
}

// 名前がこの言葉で終わるものは、秘密そのものではなく置き場所や種類（KEY_FILE・secretName など）。
var notSecretName = regexp.MustCompile(`(?i)(file|path|dir|name|type|url|uri|size|count|len|length|mode|env|id|ids|limit|ttl|expires?|at|ref|refs)["'\\]*\s*[:=]`)

// 値の形が秘密らしくないもの: パス・コードの式（p.foo・関数）
var codeish = regexp.MustCompile(`^[~./]|/|^[A-Za-z_][A-Za-z0-9_]*\.[A-Za-z_]`)

// secretish は name=value の value が秘密らしいか。8文字以上で、小文字・大文字・数字・記号のうち2種類以上。
func secretish(name, v string) bool {
	if notSecretName.MatchString(name) || codeish.MatchString(v) || len(v) < 8 {
		return false
	}
	var lo, up, dg, sy int
	for _, r := range v {
		switch {
		case r >= 'a' && r <= 'z':
			lo = 1
		case r >= 'A' && r <= 'Z':
			up = 1
		case r >= '0' && r <= '9':
			dg = 1
		default:
			sy = 1
		}
	}
	return lo+up+dg+sy >= 2
}

// luhn はカード番号の検査用の数字が合うか。
func luhn(s string) bool {
	var d []int
	for _, r := range s {
		if r >= '0' && r <= '9' {
			d = append(d, int(r-'0'))
		}
	}
	if len(d) < 13 || len(d) > 19 || d[0] < 3 || d[0] > 6 {
		return false
	}
	// 同じ数字だけ（0000…）は番号ではない
	same := true
	for _, x := range d {
		if x != d[0] {
			same = false
		}
	}
	if same {
		return false
	}
	sum := 0
	for i := len(d) - 1; i >= 0; i-- {
		x := d[i]
		if (len(d)-1-i)%2 == 1 {
			x *= 2
			if x > 9 {
				x -= 9
			}
		}
		sum += x
	}
	return sum%10 == 0
}

// placeholderish は値が秘密でないもの（数・例の書き方 xxx・${VAR}・*** など）か。
// 数を消すと JSON の数が文字になって壊れるので、数だけの値は消さない。
func placeholderish(v string) bool {
	l := strings.ToLower(v)
	if strings.Trim(v, "0123456789.-+eE") == "" || strings.HasPrefix(v, "[") {
		return true
	}
	if strings.HasPrefix(v, "$") || strings.HasPrefix(v, "%") || strings.Trim(v, "*x.X") == "" {
		return true
	}
	for _, p := range []string{"your", "example", "xxxx", "changeme", "placeholder", "redacted", "process.env", "os.environ", "getenv", "none", "null", "undefined", "true", "false", "required", "optional", "string"} {
		if strings.Contains(l, p) {
			return true
		}
	}
	return false
}
