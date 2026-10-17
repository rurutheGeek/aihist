// Package ingest はツールごとの元データを読み、変わった会話だけを store へ入れる。
package ingest

import (
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/rurutheGeek/aihist/internal/adapter/antigravity"
	"github.com/rurutheGeek/aihist/internal/adapter/chatgpt"
	"github.com/rurutheGeek/aihist/internal/adapter/claudeai"
	"github.com/rurutheGeek/aihist/internal/adapter/claudecode"
	"github.com/rurutheGeek/aihist/internal/adapter/codex"
	"github.com/rurutheGeek/aihist/internal/adapter/gemini"
	"github.com/rurutheGeek/aihist/internal/adapter/opencode"
	"github.com/rurutheGeek/aihist/internal/adapter/webexport"
	"github.com/rurutheGeek/aihist/internal/model"
	"github.com/rurutheGeek/aihist/internal/store"
)

// Result は取り込んだ件数と、変わっていなかった件数。
type Result struct {
	Imported, Skipped int

	// まだ書き込んでいない会話。HDD の上では確定のたびに待つので、まとめて書く
	pending []*model.Conversation
	stamps  []string
	size    int

	progress Progress
	done     int
}

// Progress は取り込みの進み具合（済んだ数・全体の数）を受け取る。
type Progress func(done, total int)

// tick は1つ済むたびに呼ぶ。
func (r *Result) tick(total int) {
	r.done++
	if r.progress != nil {
		r.progress(r.done, total)
	}
}

// まとめて書く上限（件数と、本文のおおよその大きさ）。
const (
	batchItems = 100
	batchBytes = 32 << 20
)

func (r *Result) flush(st *store.Store) error {
	if len(r.pending) == 0 {
		return nil
	}
	err := st.PutMany(r.pending, r.stamps)
	r.pending, r.stamps, r.size = nil, nil, 0
	return err
}

func (r Result) String() string {
	return fmt.Sprintf("%d 件取り込み、%d 件は変更なし", r.Imported, r.Skipped)
}

// Run は tool の元データ（dir）を取り込む。dir の形は各ツールの既定の置き場所と同じ。
func Run(st *store.Store, tool, host, dir string) (Result, error) {
	return RunProgress(st, tool, host, dir, nil)
}

// RunProgress は Run と同じで、進み具合を p に知らせる。
func RunProgress(st *store.Store, tool, host, dir string, p Progress) (Result, error) {
	r, err := run(st, tool, host, dir, p)
	if ferr := r.flush(st); err == nil {
		err = ferr
	}
	return r, err
}

func run(st *store.Store, tool, host, dir string, p Progress) (Result, error) {
	switch tool {
	case claudecode.Tool:
		return claudeWith(st, host, dir, p)
	case codex.Tool:
		return codexWith(st, host, dir, p)
	case antigravity.Tool:
		return antigravityWith(st, host, dir, p)
	case opencode.Tool:
		return OpenCode(st, host, dir)
	case chatgpt.Tool:
		return webExportWith(st, chatgpt.Tool, host, dir, p)
	case claudeai.Tool:
		return webExportWith(st, claudeai.Tool, host, dir, p)
	case gemini.Tool:
		return geminiWith(st, host, dir, p)
	}
	return Result{}, fmt.Errorf("知らないツール: %s", tool)
}

func Claude(st *store.Store, host, dir string) (Result, error) {
	return claudeWith(st, host, dir, nil)
}

func claudeWith(st *store.Store, host, dir string, p Progress) (Result, error) {
	r := Result{progress: p}
	files, err := claudecode.Files(dir)
	if err != nil {
		return r, err
	}
	for _, f := range files {
		r.tick(len(files))
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
	return codexWith(st, host, dir, nil)
}

func codexWith(st *store.Store, host, dir string, p Progress) (Result, error) {
	r := Result{progress: p}
	sessions, err := codex.Sessions(filepath.Join(dir, "sessions"))
	if err != nil {
		return r, err
	}
	titles := codex.Titles(filepath.Join(dir, "session_index.jsonl"))
	for _, s := range sessions {
		r.tick(len(sessions))
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
	return antigravityWith(st, host, dir, nil)
}

func antigravityWith(st *store.Store, host, dir string, p Progress) (Result, error) {
	r := Result{progress: p}
	files, err := antigravity.Files(dir)
	if err != nil {
		return r, err
	}
	for _, f := range files {
		r.tick(len(files))
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

// OpenCode は path が DB ファイルならそれを直接読み、ディレクトリなら送られた原本（*.json）を読む。
func OpenCode(st *store.Store, host, path string) (Result, error) {
	if fi, err := os.Stat(path); err == nil && fi.IsDir() {
		return openCodeFiles(st, host, path)
	}
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

func openCodeFiles(st *store.Store, host, dir string) (Result, error) {
	var r Result
	files, err := opencode.Files(dir)
	if err != nil {
		return r, err
	}
	for _, f := range files {
		id := strings.TrimSuffix(filepath.Base(f), ".json")
		stamp := fileStamp(f)
		if st.Stamp(opencode.Tool, host, id) == stamp {
			r.Skipped++
			continue
		}
		rows, err := opencode.ReadFile(f)
		if err != nil {
			log.Printf("%s: %v", f, err)
			continue
		}
		c := opencode.Convert(rows)
		c.SourcePath = f
		if err := put(st, host, c, stamp, &r); err != nil {
			return r, err
		}
	}
	return r, nil
}

type webConv struct {
	stamp, src string
	raw        []byte
}

// WebExport は Web のチャットの書き出し（ChatGPT・claude.ai）を読む。同じ会話が
// 複数の書き出しにあれば、後に置かれた書き出しのものを使う。
func WebExport(st *store.Store, tool, host, dir string) (Result, error) {
	return webExportWith(st, tool, host, dir, nil)
}

func webExportWith(st *store.Store, tool, host, dir string, p Progress) (Result, error) {
	r := Result{progress: p}
	files, err := webexport.Files(dir)
	if err != nil {
		return r, err
	}
	byID := map[string]webConv{}
	var order []string
	for _, f := range files {
		data, err := webexport.Conversations(f)
		if err != nil {
			log.Printf("%s: %v", f, err)
			continue
		}
		var ids, stamps []string
		var raws [][]byte
		if tool == chatgpt.Tool {
			cs, err := chatgpt.Split(data)
			if err != nil {
				log.Printf("%s: %v", f, err)
				continue
			}
			for _, c := range cs {
				ids, stamps, raws = append(ids, c.ID), append(stamps, c.Stamp), append(raws, c.Raw)
			}
		} else {
			cs, err := claudeai.Split(data)
			if err != nil {
				log.Printf("%s: %v", f, err)
				continue
			}
			for _, c := range cs {
				ids, stamps, raws = append(ids, c.ID), append(stamps, c.Stamp), append(raws, c.Raw)
			}
		}
		for i, id := range ids {
			if _, ok := byID[id]; !ok {
				order = append(order, id)
			}
			byID[id] = webConv{stamps[i], f, raws[i]}
		}
	}
	for _, id := range order {
		r.tick(len(order))
		w := byID[id]
		if st.Stamp(tool, host, id) == w.stamp {
			r.Skipped++
			continue
		}
		var c *model.Conversation
		if tool == chatgpt.Tool {
			c, err = chatgpt.Parse(w.raw)
		} else {
			c, err = claudeai.Parse(w.raw)
		}
		if err != nil {
			log.Printf("%s: %v", id, err)
			continue
		}
		c.SourcePath = w.src
		if err := put(st, host, c, w.stamp, &r); err != nil {
			return r, err
		}
	}
	return r, nil
}

func fileStamp(path string) string {
	fi, err := os.Stat(path)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%d:%d", fi.Size(), fi.ModTime().UnixNano())
}

func put(st *store.Store, host string, c *model.Conversation, stamp string, r *Result) error {
	if len(c.Messages) == 0 {
		return nil
	}
	c.Host = host
	r.pending, r.stamps = append(r.pending, c), append(r.stamps, stamp)
	for _, m := range c.Messages {
		for _, p := range m.Parts {
			r.size += len(p.Text)
		}
	}
	r.Imported++
	if len(r.pending) >= batchItems || r.size >= batchBytes {
		return r.flush(st)
	}
	return nil
}

// Gemini は Takeout から取り出したマイ アクティビティの HTML を読む。何度上げても、同じ件は1つにまとめる。
func Gemini(st *store.Store, host, dir string) (Result, error) {
	return geminiWith(st, host, dir, nil)
}

func geminiWith(st *store.Store, host, dir string, p Progress) (Result, error) {
	r := Result{progress: p}
	var es []gemini.Entry
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".html") {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		es = append(es, gemini.Entries(string(b))...)
		return nil
	})
	if err != nil {
		return r, err
	}
	groups := gemini.Group(es)
	for _, c := range groups {
		r.tick(len(groups))
		if st.Stamp(gemini.Tool, host, c.ID) == c.Stamp {
			r.Skipped++
			continue
		}
		if err := put(st, host, gemini.Parse(c), c.Stamp, &r); err != nil {
			return r, err
		}
	}
	return r, nil
}
