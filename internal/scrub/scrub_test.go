package scrub

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestText(t *testing.T) {
	all := strings.Join(Kinds, ",")
	cases := []struct{ list, in, want string }{
		{Secret, "key sk-proj-abcdefghijklmnopqrstuvwxyz0123 ok", "key [トークン] ok"},
		{Secret, "ghp_abcdefghijklmnopqrstuvwxyz0123456789AB", "[トークン]"},
		{Secret, "AKIAABCDEFGHIJKLMNOP", "[トークン]"},
		{Secret, "Authorization: Bearer abcdefghijklmnopqrstuvwxyz012345", "Authorization: Bearer [トークン]"},
		{Secret, "password=hunter2abc&x=1", "password=[トークン]&x=1"},
		{Secret, `"api_key": "abcd1234efgh"`, `"api_key": "[トークン]"`},
		{Secret, "postgres://app:s3cretpass@db:5432/x", "postgres://app:[トークン]@db:5432/x"},
		{Secret, "-----BEGIN OPENSSH PRIVATE KEY-----\\nb3BlbnNzaC1r\\n-----END OPENSSH PRIVATE KEY-----", "[トークン]"},
		{Secret, "password=${DB_PASSWORD}", "password=${DB_PASSWORD}"},
		{Secret, "max_tokens=409600", "max_tokens=409600"},
		{Secret, "author: ruru1234", "author: ruru1234"},
		{Email, "mail me: a.b+c@example.co.jp.", "mail me: [メール]."},
		{Phone, "電話は 03-1234-5678 か 090-1234-5678", "電話は [電話番号] か [電話番号]"},
		{Phone, "2026-10-17 と v0.18.4", "2026-10-17 と v0.18.4"},
		{Address, "〒150-0001 東京都渋谷区神宮前1-2-3 に住む", "[住所] に住む"},
		{Card, "card 4111 1111 1111 1111 end", "card [カード番号] end"},
		{Card, "ts 1792253537000", "ts 1792253537000"},
		{IP, "host 192.168.10.101 and fe80::1ff:fe23:4567:890a", "host [IPアドレス] and [IPアドレス]"},
		{IP, "std::vector and 10:00:00 and Chrome 120.0.6099.109", "std::vector and 10:00:00 and Chrome 120.0.6099.109"},
		{Secret, "nothing", "nothing"},
		{Secret, "ANSIBLE_PRIVATE_KEY_FILE=~/.ssh/id_ed25519 x", "ANSIBLE_PRIVATE_KEY_FILE=~/.ssh/id_ed25519 x"},
		{Secret, "tokenize='trigram'", "tokenize='trigram'"},
		{Secret, "secretName: aihist-tls", "secretName: aihist-tls"},
		{Secret, "AccessKeyID: p.accessKey,", "AccessKeyID: p.accessKey,"},
		{Secret, "PWD=/home/ruru ;", "PWD=/home/ruru ;"},
		{Secret, "password = getPassword()", "password = getPassword()"},
		{Secret, "token not exist, token = Abc123xyz9", "token not exist, token = [トークン]"},
		{Secret, `"a private key": beginKey + "\nabc\n-----END OPENSSH PRIVATE KEY-----", "x"`, `"a private key": beginKey + "\nabc\n-----END OPENSSH PRIVATE KEY-----", "x"`},
		{"-", "sk-proj-abcdefghijklmnopqrstuvwxyz0123", "sk-proj-abcdefghijklmnopqrstuvwxyz0123"},
		{all, "abc", "abc"},
	}
	for _, c := range cases {
		if got := Text(c.in, c.list); got != c.want {
			t.Errorf("%s %q\n got %q\nwant %q", c.list, c.in, got, c.want)
		}
	}
}

// 消したあとも JSON として読め、base64 の中身は変わらない。
func TestJSONSafe(t *testing.T) {
	all := strings.Join(Kinds, ",")
	line := `{"message":{"content":[{"type":"text","text":"pw: \"hunter2xyz\" token=abcdef123456 mail x@y.com ip 10.0.0.1 tel 03-1234-5678"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"iVBORw0KGgoAAAANSUhEUgAKIAABCDEFGHIJKLMNOP1234567890abcd+/sk-x"}}],"usage":{"input_tokens":123456,"cache_read_input_tokens":987654}}}`
	out := Line([]byte(line), all)
	var v map[string]any
	if err := json.Unmarshal(out, &v); err != nil {
		t.Fatalf("壊れた: %v\n%s", err, out)
	}
	s := string(out)
	for _, bad := range []string{"hunter2xyz", "abcdef123456", "x@y.com", "10.0.0.1", "03-1234-5678"} {
		if strings.Contains(s, bad) {
			t.Errorf("残った %q\n%s", bad, s)
		}
	}
	if !strings.Contains(s, "iVBORw0KGgoAAAANSUhEUgAKIAABCDEFGHIJKLMNOP1234567890abcd") || !strings.Contains(s, `"input_tokens":123456`) {
		t.Errorf("消しすぎ\n%s", s)
	}
}
