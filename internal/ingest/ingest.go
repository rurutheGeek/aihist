// Package ingest はツールごとの元データを読み、変わった会話だけを store へ入れる。
package ingest

import (
	"fmt"
	"log"
	"path/filepath"

	"github.com/rurutheGeek/aihist/internal/adapter/antigravity"
	"github.com/rurutheGeek/aihist/internal/adapter/claudecode"
	"github.com/rurutheGeek/aihist/internal/adapter/codex"
	"github.com/rurutheGeek/aihist/internal/adapter/opencode"
	"github.com/rurutheGeek/aihist/internal/model"
	"github.com/rurutheGeek/aihist/internal/store"
)

// Result は取り込んだ件数と、変わっていなかった件数。
type Result struct{ Imported, Skipped int }

func (r Result) String() string {
	return fmt.Sprintf("%d 件取り込み、%d 件は変更なし", r.Imported, r.Skipped)
}

// Run は tool の元データ（dir）を取り込む。dir の形は各ツールの既定の置き場所と同じ。
func Run(st *store.Store, tool, host, dir string) (Result, error) {
	switch tool {
	case claudecode.Tool:
		return Claude(st, host, dir)
	case codex.Tool:
		return Codex(st, host, dir)
	case antigravity.Tool:
		return Antigravity(st, host, dir)
	case opencode.Tool:
		return OpenCode(st, host, dir)
	}
	return Result{}, fmt.Errorf("知らないツール: %s", tool)
}

func Claude(st *store.Store, host, dir string) (Result, error) {
	var r Result
	files, err := claudecode.Files(dir)
	if err != nil {
		return r, err
	}
	for _, f := range files {
		id, _ := claudecode.ExternalID(f)
		stamp := claudecode.Stamp(f)
		if st.Stamp(claudecode.Tool, host, id) == stamp {
			r.Skipped++
			continue
		}
		c, err := claudecode.Parse(f)
		if err != nil {
			log.Printf("%s: %v", f, err)
			continue
		}
		if err := put(st, host, c, stamp, &r); err != nil {
			return r, err
		}
	}
	return r, nil
}

func Codex(st *store.Store, host, dir string) (Result, error) {
	var r Result
	sessions, err := codex.Sessions(filepath.Join(dir, "sessions"))
	if err != nil {
		return r, err
	}
	titles := codex.Titles(filepath.Join(dir, "session_index.jsonl"))
	for _, s := range sessions {
		stamp := s.Stamp() + titles[s.ID]
		if st.Stamp(codex.Tool, host, s.ID) == stamp {
			r.Skipped++
			continue
		}
		c, err := codex.Load(s)
		if err != nil {
			log.Printf("%s: %v", s.ID, err)
			continue
		}
		if t := titles[s.ID]; t != "" {
			c.Title = t
		}
		if err := put(st, host, c, stamp, &r); err != nil {
			return r, err
		}
	}
	return r, nil
}

func Antigravity(st *store.Store, host, dir string) (Result, error) {
	var r Result
	files, err := antigravity.Files(dir)
	if err != nil {
		return r, err
	}
	for _, f := range files {
		id, stamp := antigravity.ExternalID(f), antigravity.Stamp(f)
		if st.Stamp(antigravity.Tool, host, id) == stamp {
			r.Skipped++
			continue
		}
		c, err := antigravity.Parse(f)
		if err != nil {
			log.Printf("%s: %v", f, err)
			continue
		}
		if err := put(st, host, c, stamp, &r); err != nil {
			return r, err
		}
	}
	return r, nil
}

// OpenCode は DB ファイル（opencode.db）を直接読む。dir は DB のパス。
func OpenCode(st *store.Store, host, path string) (Result, error) {
	var r Result
	db, err := opencode.Open(path)
	if err != nil {
		return r, err
	}
	defer db.Close()
	sessions, err := db.Sessions()
	if err != nil {
		return r, err
	}
	for _, s := range sessions {
		if st.Stamp(opencode.Tool, host, s.ID) == s.Stamp() {
			r.Skipped++
			continue
		}
		c, err := db.Load(s)
		if err != nil {
			return r, fmt.Errorf("%s: %w", s.ID, err)
		}
		c.SourcePath = path
		if err := put(st, host, c, s.Stamp(), &r); err != nil {
			return r, err
		}
	}
	return r, nil
}

func put(st *store.Store, host string, c *model.Conversation, stamp string, r *Result) error {
	if len(c.Messages) == 0 {
		return nil
	}
	c.Host = host
	if err := st.Put(c, stamp); err != nil {
		return err
	}
	r.Imported++
	return nil
}
