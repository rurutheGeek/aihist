package web

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/rurutheGeek/aihist/internal/adapter/claudeai"
	"github.com/rurutheGeek/aihist/internal/adapter/webexport"
)

// claude.ai の書き出しは、ダウンロード先を並べた JSON（manifest）と、種類ごとの zip に分かれる。
// manifest を上げると、サーバーが zip を落とし、画面で取り込むものを選ばせる。

const exportTool = "claude_ai_export"

type manifest struct {
	CreatedAt string `json:"created_at"`
	Files     []struct {
		URL      string `json:"export_url"`
		Category string `json:"category"`
		Filename string `json:"filename"`
	} `json:"data_files"`
}

var exportID = regexp.MustCompile(`[^0-9A-Za-z]+`)

// isManifest は claude.ai の書き出しの manifest か。
func isManifest(head []byte) bool {
	s := string(head)
	return strings.Contains(s, `"data_files"`) && strings.Contains(s, `"export_url"`)
}

func readManifest(b []byte) (*manifest, error) {
	var m manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	for _, f := range m.Files {
		u, err := url.Parse(f.URL)
		// 落としに行くのは claude.ai の書き出しだけ
		if err != nil || u.Scheme != "https" || u.Host != "claude.ai" || !strings.HasPrefix(u.Path, "/export/") {
			return nil, fmt.Errorf("claude.ai の書き出しのリンクではない: %s", f.URL)
		}
		if filepath.Base(f.Filename) != f.Filename || !strings.HasSuffix(f.Filename, ".zip") {
			return nil, fmt.Errorf("ファイル名がおかしい: %s", f.Filename)
		}
	}
	return &m, nil
}

// exportState は落としている途中の様子（ファイル名ごと）。
var exportState = struct {
	sync.Mutex
	m map[string]string // dir/filename → "" 落としている途中、それ以外は誤り
}{m: map[string]string{}}

// startExport は manifest を置き、zip を順に落とし始める。書き出しの ID を返す。
func (s *Server) startExport(host string, b []byte) (string, error) {
	m, err := readManifest(b)
	if err != nil {
		return "", err
	}
	id := exportID.ReplaceAllString(m.CreatedAt, "")
	if id == "" {
		id = time.Now().Format("20060102150405")
	}
	dir, err := s.raw.Root(host, exportTool)
	if err != nil {
		return "", err
	}
	dir = filepath.Join(dir, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), b, 0o644); err != nil {
		return "", err
	}
	go s.download(dir, m)
	return id, nil
}

func (s *Server) download(dir string, m *manifest) {
	c := &http.Client{Timeout: 2 * time.Hour}
	for _, f := range m.Files {
		dst := filepath.Join(dir, f.Filename)
		key := dst
		if _, err := os.Stat(dst); err == nil {
			continue
		}
		exportState.Lock()
		if _, busy := exportState.m[key]; busy && exportState.m[key] == "" {
			exportState.Unlock()
			continue
		}
		exportState.m[key] = ""
		exportState.Unlock()
		err := fetchTo(c, f.URL, dst)
		exportState.Lock()
		if err != nil {
			log.Printf("export %s: %v", f.Filename, err)
			exportState.m[key] = err.Error()
		} else {
			delete(exportState.m, key)
		}
		exportState.Unlock()
	}
}

func fetchTo(c *http.Client, u, dst string) error {
	res, err := c.Get(u)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 300))
		if strings.Contains(string(b), "Just a moment") {
			return errors.New("claude.ai がサーバーからのダウンロードを止めている（ブラウザでしか落とせない）")
		}
		return fmt.Errorf("%s %s", res.Status, strings.TrimSpace(string(b)))
	}
	tmp := dst + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, res.Body); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if _, err := zip.OpenReader(tmp); err != nil {
		os.Remove(tmp)
		return errors.New("zip ではないものが返ってきた（リンクが切れたか、ログインが要る）")
	}
	return os.Rename(tmp, dst)
}

// 書き出しの種類と、aihist で取り込めるか。
var categories = map[string]struct {
	Label    string
	Readable bool
}{
	"conversations":     {"会話", true},
	"claude_code_cloud": {"Claude Code（クラウド）", false},
	"projects":          {"プロジェクト", false},
	"memories":          {"メモリ", false},
	"feedback":          {"フィードバック", false},
	"frames":            {"frames", false},
	"light_metadata":    {"メタデータ", false},
}

type exportFile struct {
	Category, Label, Filename string
	Readable                  bool
	State                     string // waiting / downloading / done / error / imported
	Error                     string
	Bytes                     int64
	Count                     int // 会話の数（読めたとき）
}

func (s *Server) exportDir(host, id string) (string, error) {
	if !raw0.MatchString(id) {
		return "", errors.New("bad id")
	}
	dir, err := s.raw.Root(host, exportTool)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, id), nil
}

var raw0 = regexp.MustCompile(`^[0-9A-Za-z]{1,64}$`)

func (s *Server) exportFiles(host, id string) ([]exportFile, bool, error) {
	dir, err := s.exportDir(host, id)
	if err != nil {
		return nil, false, err
	}
	b, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, false, err
	}
	m, err := readManifest(b)
	if err != nil {
		return nil, false, err
	}
	imported, _ := s.raw.Root(host, claudeai.Tool)
	busy := false
	var out []exportFile
	for _, f := range m.Files {
		c := categories[f.Category]
		if c.Label == "" {
			c.Label = f.Category
		}
		e := exportFile{Category: f.Category, Label: c.Label, Filename: f.Filename, Readable: c.Readable, State: "waiting"}
		p := filepath.Join(dir, f.Filename)
		exportState.Lock()
		msg, inState := exportState.m[p]
		exportState.Unlock()
		if st, err := os.Stat(p); err == nil {
			e.State, e.Bytes = "done", st.Size()
			if e.Readable {
				if data, err := webexport.Conversations(p); err == nil {
					if cs, err := claudeai.Split(data); err == nil {
						e.Count = len(cs)
					}
				} else {
					e.Readable = false
				}
			}
			if _, err := os.Stat(filepath.Join(imported, "export", id+"-"+f.Filename)); err == nil {
				e.State = "imported"
			}
		} else if inState && msg == "" {
			e.State, busy = "downloading", true
			if st, err := os.Stat(p + ".part"); err == nil {
				e.Bytes = st.Size()
			}
		} else if inState {
			e.State, e.Error = "error", msg
		} else {
			busy = true
		}
		out = append(out, e)
	}
	return out, busy, nil
}

// exportPage は落とした書き出しの中身を並べ、取り込むものを選ばせる。
func (s *Server) exportPage(w http.ResponseWriter, r *http.Request) {
	host, id := r.PathValue("host"), r.PathValue("id")
	files, busy, err := s.exportFiles(host, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	d, err := s.side(r, nil)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	d["Files"], d["Busy"], d["ExportHost"], d["ExportID"], d["Page"] = files, busy, host, id, "upload"
	s.render(w, r, "export.html", d)
}

// exportImport は選んだ zip を取り込む。
func (s *Server) exportImport(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	host, id := r.PathValue("host"), r.PathValue("id")
	dir, err := s.exportDir(host, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	files, _, err := s.exportFiles(host, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	r.ParseForm()
	want := map[string]bool{}
	for _, f := range r.Form["file"] {
		want[f] = true
	}
	n := 0
	for _, f := range files {
		if !want[f.Filename] || !f.Readable || f.State != "done" {
			continue
		}
		src, err := os.Open(filepath.Join(dir, f.Filename))
		if err != nil {
			continue
		}
		st, _ := src.Stat()
		err = s.raw.Put(host, claudeai.Tool, "export/"+id+"-"+f.Filename, src, st.ModTime())
		src.Close()
		if err == nil {
			n++
		}
	}
	if n > 0 {
		s.ingest(host, claudeai.Tool)
	}
	http.Redirect(w, r, "/exports/"+url.PathEscape(host)+"/"+id, http.StatusSeeOther)
}
