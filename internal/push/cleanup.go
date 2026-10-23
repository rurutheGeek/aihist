package push

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/rurutheGeek/aihist/internal/adapter/opencode"
	"github.com/rurutheGeek/aihist/internal/raw"
)

// deletable は消してよいファイルか。索引など、会話そのものでないものは消さない。
func deletable(tool, rel string) bool {
	switch tool {
	case "codex":
		return strings.HasPrefix(rel, "sessions/")
	case "antigravity":
		return strings.HasSuffix(rel, ".db")
	}
	return true
}

// Cleanup は、最終更新から days 日より前で、サーバーに同じもの（大きさと更新時刻）があるファイルを手元から消す。
// 消したファイルは aihist restore で戻せる。消した数と大きさを返す。
func (c *Client) Cleanup(src Source, days int) (int, int64, error) {
	if days <= 0 {
		return 0, 0, nil
	}
	if _, err := os.Stat(src.Dir); err != nil {
		return 0, 0, nil
	}
	var have map[string]raw.Info
	if err := c.getJSON("/api/files", url.Values{"host": {c.Host}, "tool": {src.Tool}}, &have); err != nil {
		return 0, 0, err
	}
	before := time.Now().AddDate(0, 0, -days)
	synced := func(p, rel string) bool {
		st, err := os.Stat(p)
		if err != nil {
			return false
		}
		h, ok := have[rel]
		return ok && c.same(h, st.Size(), st.ModTime().UnixNano()) && st.ModTime().Before(before)
	}
	n, size := 0, int64(0)
	var dirs []string
	err := filepath.WalkDir(src.Dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != src.Dir {
				dirs = append(dirs, p)
			}
			return nil
		}
		r, _ := filepath.Rel(src.Dir, p)
		rel := filepath.ToSlash(r)
		if !src.Want(rel) || !deletable(src.Tool, rel) || !synced(p, rel) {
			return nil
		}
		// Antigravity は WAL も同じく送り済みで古いときだけ、組で消す
		files := []string{p}
		if _, err := os.Stat(p + "-wal"); err == nil {
			if !synced(p+"-wal", rel+"-wal") {
				return nil
			}
			files = append(files, p+"-wal")
		}
		for _, f := range files {
			if st, err := os.Stat(f); err == nil && os.Remove(f) == nil {
				n++
				size += st.Size()
			}
		}
		return nil
	})
	// 空になったフォルダ（Codex の日付のフォルダなど）を深いほうから消す
	for i := len(dirs) - 1; i >= 0; i-- {
		os.Remove(dirs[i])
	}
	return n, size, err
}

// Present は手元にある会話のファイル（rel）を集めてサーバーに知らせる。サーバーは、
// 原本はあるのに手元に無い会話に「マシンから消えている」印を付ける。古いサーバーは知らないので、失敗は無視してよい。
func (c *Client) Present(src Source) error {
	if _, err := os.Stat(src.Dir); err != nil {
		return nil
	}
	var rels []string
	filepath.WalkDir(src.Dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		r, _ := filepath.Rel(src.Dir, p)
		if rel := filepath.ToSlash(r); src.Want(rel) {
			rels = append(rels, rel)
		}
		return nil
	})
	return c.present(src.Tool, rels)
}

// CleanupOpenCode は OpenCode の会話のうち、最終更新から days 日より前で、サーバーに同じ版があるものを手元から消す。
// OpenCode の DB は会話ごとのファイルではないので、DB を直接いじらず OpenCode 自身の
// 「opencode session delete」で消す（OpenCode が無ければ何もしない）。消した数を返す。
func (c *Client) CleanupOpenCode(path string, days int) (int, error) {
	if days <= 0 {
		return 0, nil
	}
	if _, err := os.Stat(path); err != nil {
		return 0, nil
	}
	bin := opencodeBin()
	if bin == "" {
		return 0, nil
	}
	var have map[string]raw.Info
	if err := c.getJSON("/api/files", url.Values{"host": {c.Host}, "tool": {opencode.Tool}}, &have); err != nil {
		return 0, err
	}
	db, err := opencode.Open(path)
	if err != nil {
		return 0, err
	}
	sessions, err := db.Sessions()
	db.Close()
	if err != nil {
		return 0, err
	}
	before := time.Now().AddDate(0, 0, -days).UnixMilli()
	n := 0
	for _, s := range sessions {
		h, ok := have[s.ID+".json"]
		if s.Updated >= before || !ok || h.MTime != time.UnixMilli(s.Updated).UnixNano() {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		err := exec.CommandContext(ctx, bin, "session", "delete", s.ID).Run()
		cancel()
		if err == nil {
			n++
		}
	}
	return n, nil
}

// opencodeBin は opencode の実行ファイル（PATH か、既定の入れ場所）。無ければ空。
func opencodeBin() string {
	if p, err := exec.LookPath("opencode"); err == nil {
		return p
	}
	home, _ := os.UserHomeDir()
	for _, p := range []string{filepath.Join(home, ".opencode", "bin", "opencode"), filepath.Join(home, ".opencode", "bin", "opencode.exe")} {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

// PresentOpenCode は OpenCode の DB にある会話を知らせる。
func (c *Client) PresentOpenCode(path string) error {
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	db, err := opencode.Open(path)
	if err != nil {
		return err
	}
	defer db.Close()
	sessions, err := db.Sessions()
	if err != nil {
		return err
	}
	rels := make([]string, 0, len(sessions))
	for _, s := range sessions {
		rels = append(rels, s.ID+".json")
	}
	return c.present(opencode.Tool, rels)
}

func (c *Client) present(tool string, rels []string) error {
	b, _ := json.Marshal(rels)
	res, err := c.req("POST", "/api/present", url.Values{"host": {c.Host}, "tool": {tool}}, &upload{bytes.NewReader(b), int64(len(b)), time.Now()})
	if err != nil {
		return err
	}
	return res.Body.Close()
}
