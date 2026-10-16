package push

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/rurutheGeek/aihist/internal/adapter/antigravity"
	"github.com/rurutheGeek/aihist/internal/adapter/claudecode"
	"github.com/rurutheGeek/aihist/internal/adapter/codex"
	"github.com/rurutheGeek/aihist/internal/adapter/opencode"
	"github.com/rurutheGeek/aihist/internal/raw"
	"github.com/rurutheGeek/aihist/internal/store"
)

// Restore は会話の原本をサーバーから取り出し、元の AI が読む場所へ戻す。
// 手元に同じ会話があって中身が違うときは、force でなければ上書きしない。
// 戻した先と、続けるためのコマンドを返す。
func (c *Client) Restore(home, id string, force bool) ([]string, string, error) {
	var conv store.ConvRow
	if err := c.getJSON("/api/find", url.Values{"id": {id}}, &conv); err != nil {
		return nil, "", err
	}
	var files map[string]raw.Info
	if err := c.getJSON("/api/files", url.Values{"host": {conv.Host}, "tool": {conv.Tool}}, &files); err != nil {
		return nil, "", err
	}
	ext := conv.ExternalID
	var picked []string
	for rel := range files {
		if belongs(conv.Tool, ext, rel) {
			picked = append(picked, rel)
		}
	}
	if len(picked) == 0 {
		return nil, "", fmt.Errorf("%s の原本がサーバーに無い（%s/%s）", ext, conv.Host, conv.Tool)
	}
	var done []string
	switch conv.Tool {
	case claudecode.Tool:
		dir := filepath.Join(home, ".claude", "projects", claudeProjectDir(conv.Cwd))
		for _, rel := range picked {
			// 本体は <会話>.jsonl、サブエージェントは <会話>/subagents/ の下
			sub := rel[strings.LastIndex(rel, "/")+1:]
			if i := strings.Index(rel, "/"+ext+"/subagents/"); i >= 0 {
				sub = rel[i+1:]
			}
			dst := filepath.Join(dir, filepath.FromSlash(sub))
			if err := c.fetch(conv, rel, dst, force); err != nil {
				return done, "", err
			}
			done = append(done, dst)
		}
		return done, fmt.Sprintf("cd %s && claude --resume %s", conv.Cwd, ext), nil
	case codex.Tool:
		for _, rel := range picked {
			if rel == "session_index.jsonl" {
				continue
			}
			dst := filepath.Join(home, ".codex", filepath.FromSlash(codexPath(rel)))
			if err := c.fetch(conv, rel, dst, force); err != nil {
				return done, "", err
			}
			done = append(done, dst)
		}
		if conv.Title != "" {
			if err := codexIndex(filepath.Join(home, ".codex", "session_index.jsonl"), ext, conv.Title); err != nil {
				return done, "", err
			}
		}
		return done, fmt.Sprintf("cd %s && codex resume %s", conv.Cwd, ext), nil
	case antigravity.Tool:
		dir := filepath.Join(home, ".gemini", "antigravity-ide", "conversations")
		for _, rel := range picked {
			dst := filepath.Join(dir, rel)
			if err := c.fetch(conv, rel, dst, force); err != nil {
				return done, "", err
			}
			done = append(done, dst)
		}
		return done, "Antigravity を開き直して会話の一覧から選ぶ", nil
	case opencode.Tool:
		tmp, err := os.CreateTemp("", "aihist-*.json")
		if err != nil {
			return nil, "", err
		}
		tmp.Close()
		defer os.Remove(tmp.Name())
		if err := c.fetch(conv, picked[0], tmp.Name(), true); err != nil {
			return nil, "", err
		}
		rows, err := opencode.ReadFile(tmp.Name())
		if err != nil {
			return nil, "", err
		}
		db := filepath.Join(home, ".local", "share", "opencode", "opencode.db")
		if err := opencode.Restore(db, rows); err != nil {
			return nil, "", err
		}
		return []string{db}, fmt.Sprintf("cd %s && opencode --session %s", conv.Cwd, ext), nil
	}
	return nil, "", fmt.Errorf("%s は戻せない", conv.Tool)
}

func belongs(tool, ext, rel string) bool {
	base := rel[strings.LastIndex(rel, "/")+1:]
	switch tool {
	case claudecode.Tool:
		return base == ext+".jsonl" || strings.Contains(rel, "/"+ext+"/subagents/")
	case codex.Tool:
		return strings.HasPrefix(base, "rollout-") && strings.Contains(base, ext)
	case antigravity.Tool:
		return base == ext+".db" || base == ext+".db-wal"
	case opencode.Tool:
		return base == ext+".json"
	}
	return false
}

// claudeProjectDir は Claude Code が作業フォルダから作るディレクトリ名（英数字以外を - にする）。
func claudeProjectDir(cwd string) string {
	return regexp.MustCompile(`[^A-Za-z0-9]`).ReplaceAllString(cwd, "-")
}

var rolloutDate = regexp.MustCompile(`^rollout-(\d{4})-(\d{2})-(\d{2})T`)

// codexPath は原本の置き場所（~/.codex からの相対）。画面から上げたものは日付の場所へ置き直す。
func codexPath(rel string) string {
	base := rel[strings.LastIndex(rel, "/")+1:]
	if m := rolloutDate.FindStringSubmatch(base); m != nil && !strings.HasPrefix(rel, "sessions/20") {
		return strings.Join([]string{"sessions", m[1], m[2], m[3], base}, "/")
	}
	return rel
}

func codexIndex(path, id, title string) error {
	if b, err := os.ReadFile(path); err == nil && strings.Contains(string(b), `"id":"`+id+`"`) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	line, _ := json.Marshal(map[string]string{"id": id, "thread_name": title, "updated_at": time.Now().UTC().Format(time.RFC3339Nano)})
	_, err = f.Write(append(line, '\n'))
	return err
}

// fetch は原本1ファイルを dst へ書く。同じ大きさのファイルがあれば何もしない。
func (c *Client) fetch(conv store.ConvRow, rel, dst string, force bool) error {
	res, err := c.req("GET", "/api/file", url.Values{"host": {conv.Host}, "tool": {conv.Tool}, "path": {rel}}, nil)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if st, err := os.Stat(dst); err == nil && !force {
		if st.Size() == res.ContentLength {
			return nil
		}
		return fmt.Errorf("%s が手元にあり中身が違う（上書きするなら -force）", dst)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".aihist-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, res.Body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dst)
}
