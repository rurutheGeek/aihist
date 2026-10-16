// Package push は手元の元データのうち、サーバーに無いか変わったファイルだけを送る。
package push

import (
	"encoding/json"
	"fmt"
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
}

func (c *Client) req(method, p string, q url.Values, body *os.File) (*http.Response, error) {
	u := strings.TrimRight(c.Server, "/") + p + "?" + q.Encode()
	var req *http.Request
	var err error
	if body != nil {
		req, err = http.NewRequest(method, u, body)
	} else {
		req, err = http.NewRequest(method, u, nil)
	}
	if err != nil {
		return nil, err
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if body != nil {
		st, err := body.Stat()
		if err != nil {
			return nil, err
		}
		req.ContentLength = st.Size()
		req.Header.Set("X-Mtime", fmt.Sprint(st.ModTime().UnixNano()))
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

// Push は src のうち変わったファイルを送り、サーバーに取り込ませる。送ったファイル数と取り込み結果を返す。
func (c *Client) Push(src Source) (int, string, error) {
	if _, err := os.Stat(src.Dir); err != nil {
		return 0, "", nil
	}
	q := url.Values{"host": {c.Host}, "tool": {src.Tool}}
	res, err := c.req("GET", "/api/files", q, nil)
	if err != nil {
		return 0, "", err
	}
	var have map[string]raw.Info
	err = json.NewDecoder(res.Body).Decode(&have)
	res.Body.Close()
	if err != nil {
		return 0, "", err
	}
	sent := 0
	err = filepath.WalkDir(src.Dir, func(p string, d fs.DirEntry, err error) error {
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
		if h, ok := have[rel]; ok && h.Size == st.Size() && h.MTime == st.ModTime().UnixNano() {
			return nil
		}
		// 書き込み中の Antigravity の DB なども開ける（共有モードで読むだけ）
		f, err := os.Open(p)
		if err != nil {
			return nil
		}
		defer f.Close()
		q := url.Values{"host": {c.Host}, "tool": {src.Tool}, "path": {rel}}
		res, err := c.req("PUT", "/api/files", q, f)
		if err != nil {
			return err
		}
		res.Body.Close()
		sent++
		return nil
	})
	if err != nil || sent == 0 {
		return sent, "", err
	}
	res, err = c.req("POST", "/api/import", q, nil)
	if err != nil {
		return sent, "", err
	}
	defer res.Body.Close()
	var out struct{ Result string }
	json.NewDecoder(res.Body).Decode(&out)
	return sent, out.Result, nil
}

// NewClient は既定の HTTP クライアントで作る。
func NewClient(server, token, host string) *Client {
	return &Client{Server: server, Token: token, Host: host, HTTP: &http.Client{Timeout: 10 * time.Minute}}
}
