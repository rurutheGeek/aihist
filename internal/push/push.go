// Package push は手元の元データのうち、サーバーに無いか変わったファイルだけを送る。
package push

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rurutheGeek/aihist/internal/adapter/antigravity"
	"github.com/rurutheGeek/aihist/internal/adapter/claudecode"
	"github.com/rurutheGeek/aihist/internal/adapter/codex"
	"github.com/rurutheGeek/aihist/internal/adapter/opencode"
	"github.com/rurutheGeek/aihist/internal/raw"
)

// Source は1ツールぶんの手元の置き場所と、送る対象のファイルの選び方。
type Source struct {
	Tool string
	Dir  string
	Want func(rel string) bool
}

// Defaults は各ツールの既定の置き場所。
func Defaults(home string) []Source {
	return []Source{
		{claudecode.Tool, filepath.Join(home, ".claude", "projects"), func(rel string) bool {
			return strings.HasSuffix(rel, ".jsonl")
		}},
		{codex.Tool, filepath.Join(home, ".codex"), func(rel string) bool {
			return rel == "session_index.jsonl" ||
				strings.HasPrefix(rel, "sessions/") && strings.HasPrefix(path(rel), "rollout-") && strings.HasSuffix(rel, ".jsonl")
		}},
		{antigravity.Tool, filepath.Join(home, ".gemini", "antigravity-ide", "conversations"), func(rel string) bool {
			return !strings.Contains(rel, "/") && (strings.HasSuffix(rel, ".db") || strings.HasSuffix(rel, ".db-wal"))
		}},
	}
}

func path(rel string) string { return rel[strings.LastIndex(rel, "/")+1:] }

type Client struct {
	Server string // http://host:port
	Token  string
	Host   string
	HTTP   *http.Client
	// Attach は送る添付ファイルの種類（"*" は全部、"*,-video" は動画以外、"-" は送らない）。
	// 送らない種類は、埋め込まれた中身を抜いて送る
	Attach string
	// Hide は送る前に消す情報の種類（"secret,email" など。"-" は消さない）。
	Hide string
}

// upload は body を1ファイルとして原本の置き場所へ送る。
type upload struct {
	body  io.Reader
	size  int64
	mtime time.Time
}

func (c *Client) req(method, p string, q url.Values, up *upload) (*http.Response, error) {
	u := strings.TrimRight(c.Server, "/") + p + "?" + q.Encode()
	var body io.Reader
	if up != nil {
		body = up.body
	}
	req, err := http.NewRequest(method, u, body)
	if err != nil {
		return nil, err
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if up != nil {
		req.ContentLength = up.size
		req.Header.Set("X-Mtime", fmt.Sprint(up.mtime.UnixNano()))
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode/100 != 2 {
		var b [512]byte
		n, _ := res.Body.Read(b[:])
		res.Body.Close()
		return nil, fmt.Errorf("%s %s: %s %s", method, p, res.Status, strings.TrimSpace(string(b[:n])))
	}
	return res, nil
}

func (c *Client) getJSON(p string, q url.Values, v any) error {
	res, err := c.req("GET", p, q, nil)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	return json.NewDecoder(res.Body).Decode(v)
}

// Imported はサーバーの取り込みの結果。古いサーバーは件数を返さない（Imported が -1 で Text だけ）。
type Imported struct {
	Imported, Skipped int
	Text              string
}

func (c *Client) importNow(tool string) (Imported, error) {
	res, err := c.req("POST", "/api/import", url.Values{"host": {c.Host}, "tool": {tool}}, nil)
	if err != nil {
		return Imported{}, err
	}
	defer res.Body.Close()
	out := struct {
		Result            string
		Imported, Skipped *int
	}{}
	json.NewDecoder(res.Body).Decode(&out)
	r := Imported{Imported: -1, Text: out.Result}
	if out.Imported != nil && out.Skipped != nil {
		r.Imported, r.Skipped = *out.Imported, *out.Skipped
	}
	return r, nil
}

// Push は src のうち変わったファイルを送り、サーバーに取り込ませる。送ったファイル数と取り込み結果を返す。
func (c *Client) Push(src Source) (int, Imported, error) {
	if _, err := os.Stat(src.Dir); err != nil {
		return 0, Imported{}, nil
	}
	var have map[string]raw.Info
	if err := c.getJSON("/api/files", url.Values{"host": {c.Host}, "tool": {src.Tool}}, &have); err != nil {
		return 0, Imported{}, err
	}
	sent := 0
	err := filepath.WalkDir(src.Dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		r, _ := filepath.Rel(src.Dir, p)
		rel := filepath.ToSlash(r)
		if !src.Want(rel) {
			return nil
		}
		st, err := d.Info()
		if err != nil {
			return err
		}
		if h, ok := have[rel]; ok && c.same(h, st.Size(), st.ModTime().UnixNano()) {
			return nil
		}
		// 書き込み中の Antigravity の DB なども開ける（共有モードで読むだけ）
		f, err := os.Open(p)
		if err != nil {
			return nil
		}
		defer f.Close()
		q := url.Values{"host": {c.Host}, "tool": {src.Tool}, "path": {rel}}
		up := &upload{f, st.Size(), st.ModTime()}
		// Antigravity の DB は形を変えられないので、そのまま送る
		if c.rewrites() && src.Tool != antigravity.Tool {
			t, size, err := stripped(f, c.Attach, c.Hide)
			if err != nil {
				return err
			}
			defer drop(t)
			up = &upload{t, size, st.ModTime()}
		}
		res, err := c.req("PUT", "/api/files", q, up)
		if err != nil {
			return err
		}
		res.Body.Close()
		sent++
		return nil
	})
	if err != nil || sent == 0 {
		return sent, Imported{}, err
	}
	out, err := c.importNow(src.Tool)
	return sent, out, err
}

// PushOpenCode は OpenCode の DB から、変わった会話の原本（行そのまま）を書き出して送る。
func (c *Client) PushOpenCode(path string) (int, Imported, error) {
	if _, err := os.Stat(path); err != nil {
		return 0, Imported{}, nil
	}
	db, err := opencode.Open(path)
	if err != nil {
		return 0, Imported{}, err
	}
	defer db.Close()
	var have map[string]raw.Info
	if err := c.getJSON("/api/files", url.Values{"host": {c.Host}, "tool": {opencode.Tool}}, &have); err != nil {
		return 0, Imported{}, err
	}
	sessions, err := db.Sessions()
	if err != nil {
		return 0, Imported{}, err
	}
	sent := 0
	for _, s := range sessions {
		rel := s.ID + ".json"
		mt := time.UnixMilli(s.Updated)
		if h, ok := have[rel]; ok && h.MTime == mt.UnixNano() {
			continue
		}
		if err := c.sendOpenCode(db, s.ID, rel, mt); err != nil {
			return sent, Imported{}, fmt.Errorf("%s: %w", s.ID, err)
		}
		sent++
	}
	if sent == 0 {
		return 0, Imported{}, nil
	}
	out, err := c.importNow(opencode.Tool)
	return sent, out, err
}

// sendOpenCode は1会話の原本を一時ファイルへ書き出してから送る（大きい会話をメモリに載せない）。
func (c *Client) sendOpenCode(db *opencode.DB, id, rel string, mt time.Time) error {
	f, err := os.CreateTemp("", "aihist-opencode-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err := db.ExportTo(f, id); err != nil {
		return err
	}
	size, err := f.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	var body io.Reader = f
	if c.rewrites() {
		t, n, err := stripped(f, c.Attach, c.Hide)
		if err != nil {
			return err
		}
		defer drop(t)
		body, size = t, n
	}
	q := url.Values{"host": {c.Host}, "tool": {opencode.Tool}, "path": {rel}}
	res, err := c.req("PUT", "/api/files", q, &upload{body, size, mt})
	if err != nil {
		return err
	}
	return res.Body.Close()
}

// NewClient は既定の HTTP クライアントで作る。
func NewClient(server, token, host string) *Client {
	return &Client{Server: server, Token: token, Host: host, HTTP: &http.Client{Timeout: 10 * time.Minute}}
}
