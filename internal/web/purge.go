package web

import (
	"archive/zip"
	"bufio"
	"encoding/json"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rurutheGeek/aihist/internal/scrub"
)

// purge は集めた履歴（データベースとサーバーの原本）から、選んだ種類の情報を消す。元には戻せない。
// 裏で進め、進み具合は取り込みと同じ /upload/jobs/{id} で見る。
func (s *Server) purge(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	r.ParseForm()
	list := hideList(r.Form["hide"])
	if !scrub.Any(list) {
		http.Error(w, "nothing", 400)
		return
	}
	id := newJobID()
	j := &job{State: "waiting"}
	jobs.Lock()
	jobs.m[id] = j
	jobs.Unlock()
	go func() {
		// 取り込みと同時に書き換えない
		s.mu.Lock()
		defer s.mu.Unlock()
		set := func(f func()) { jobs.Lock(); f(); jobs.Unlock() }
		set(func() { j.State = "running" })
		fn := func(t string) string { return scrub.Text(t, list) }
		n, err := s.st.Rewrite(fn, func(done, total int) { set(func() { j.Done, j.Total = done, total }) })
		if err == nil {
			var files int
			files, err = s.purgeRaw(list)
			log.Printf("purge %s: %d parts, %d files %v", list, n, files, err)
		}
		set(func() {
			j.State, j.Imported = "done", n
			if err != nil {
				j.State, j.Error = "error", err.Error()
			}
		})
		time.AfterFunc(time.Hour, func() { jobs.Lock(); delete(jobs.m, id); jobs.Unlock() })
	}()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"id": id})
}

// 文字の原本（行ごとに書き換えられるもの）
func textFile(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".jsonl", ".json", ".html", ".htm", ".md", ".txt":
		return true
	}
	return false
}

// purgeRaw はサーバーの原本を書き換える。Antigravity の DB は形を変えられないので触らない。
// 更新時刻は元のままにする（マシンが同じファイルを送り直さないよう）。
func (s *Server) purgeRaw(list string) (int, error) {
	n := 0
	err := filepath.WalkDir(s.raw.Dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		var ch bool
		switch {
		case textFile(p):
			ch, err = rewriteFile(p, func(r io.Reader, w io.Writer) (bool, error) { return scrubLines(r, w, list) })
		case strings.EqualFold(filepath.Ext(p), ".zip"):
			ch, err = rewriteFile(p, func(r io.Reader, w io.Writer) (bool, error) { return scrubZip(r.(*os.File), w, list) })
		}
		if err != nil {
			log.Printf("purge %s: %v", p, err)
			return nil
		}
		if ch {
			n++
		}
		return nil
	})
	return n, err
}

// rewriteFile は p を fn で書き換えた写しに置き換える。変わらなければ何もしない。
func rewriteFile(p string, fn func(io.Reader, io.Writer) (bool, error)) (bool, error) {
	st, err := os.Stat(p)
	if err != nil {
		return false, err
	}
	f, err := os.Open(p)
	if err != nil {
		return false, err
	}
	defer f.Close()
	tmp, err := os.CreateTemp(filepath.Dir(p), ".purge-*")
	if err != nil {
		return false, err
	}
	defer os.Remove(tmp.Name())
	ch, err := fn(f, tmp)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil || !ch {
		return false, err
	}
	// 書き換えている間に送られてきたら、新しい方を残す
	if now, err := os.Stat(p); err != nil || !now.ModTime().Equal(st.ModTime()) {
		return false, err
	}
	if err := os.Chtimes(tmp.Name(), st.ModTime(), st.ModTime()); err != nil {
		return false, err
	}
	return true, os.Rename(tmp.Name(), p)
}

func scrubLines(r io.Reader, w io.Writer, list string) (bool, error) {
	br := bufio.NewReaderSize(r, 1<<20)
	bw := bufio.NewWriterSize(w, 1<<20)
	ch := false
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			out := scrub.Line(line, list)
			if string(out) != string(line) {
				ch = true
			}
			if _, werr := bw.Write(out); werr != nil {
				return false, werr
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return false, err
		}
	}
	return ch, bw.Flush()
}

// scrubZip は書き出しの zip の中の文字のファイルを書き換える。ほかのファイルは圧縮したまま写す。
func scrubZip(f *os.File, w io.Writer, list string) (bool, error) {
	st, err := f.Stat()
	if err != nil {
		return false, err
	}
	zr, err := zip.NewReader(f, st.Size())
	if err != nil {
		return false, err
	}
	zw := zip.NewWriter(w)
	ch := false
	for _, e := range zr.File {
		if !textFile(e.Name) || e.FileInfo().IsDir() {
			raw, err := e.OpenRaw()
			if err != nil {
				return false, err
			}
			out, err := zw.CreateRaw(&e.FileHeader)
			if err != nil {
				return false, err
			}
			if _, err := io.Copy(out, raw); err != nil {
				return false, err
			}
			continue
		}
		in, err := e.Open()
		if err != nil {
			return false, err
		}
		out, err := zw.CreateHeader(&zip.FileHeader{Name: e.Name, Method: zip.Deflate, Modified: e.Modified, Comment: e.Comment})
		if err != nil {
			in.Close()
			return false, err
		}
		c, err := scrubLines(in, out, list)
		in.Close()
		if err != nil {
			return false, err
		}
		ch = ch || c
	}
	return ch, zw.Close()
}

// putUpload は画面から上げた原本を置き、設定の「取り込む前に消す情報」を消す。
func (s *Server) putUpload(host, tool, rel string, r io.Reader, mtime time.Time) error {
	if err := s.raw.Put(host, tool, rel, r, mtime); err != nil {
		return err
	}
	p, err := s.raw.Path(host, tool, rel)
	if err != nil {
		return err
	}
	return s.hideUploaded(p)
}

// hideUploaded は置いた原本 p から、アップロードで消す種類の情報を消す。
func (s *Server) hideUploaded(p string) error {
	list := s.uploadHide()
	if !scrub.Any(list) {
		return nil
	}
	var err error
	switch {
	case textFile(p):
		_, err = rewriteFile(p, func(r io.Reader, w io.Writer) (bool, error) { return scrubLines(r, w, list) })
	case strings.EqualFold(filepath.Ext(p), ".zip"):
		_, err = rewriteFile(p, func(r io.Reader, w io.Writer) (bool, error) { return scrubZip(r.(*os.File), w, list) })
	}
	return err
}
