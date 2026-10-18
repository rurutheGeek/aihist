package web

import (
	"archive/zip"
	"bufio"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rurutheGeek/aihist/internal/adapter/antigravity"
	"github.com/rurutheGeek/aihist/internal/adapter/chatgpt"
	"github.com/rurutheGeek/aihist/internal/adapter/claudeai"
	"github.com/rurutheGeek/aihist/internal/adapter/claudecode"
	"github.com/rurutheGeek/aihist/internal/adapter/codex"
	"github.com/rurutheGeek/aihist/internal/adapter/gemini"
	"github.com/rurutheGeek/aihist/internal/adapter/webexport"
	"github.com/rurutheGeek/aihist/internal/ingest"
	"github.com/rurutheGeek/aihist/internal/push"
)

// auth は /api に、設定の画面で発行したトークンか、起動時に渡したトークン（AIHIST_TOKEN）を求める。
func (s *Server) auth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		ok := s.token != "" && subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) == 1
		if !ok && !s.st.CheckToken(got) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h(w, r)
	}
}

// sameOrigin は画面からの POST が、別のサイトから送らされたものでないかを確かめる。
func sameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		o = r.Header.Get("Referer")
	}
	u, err := url.Parse(o)
	return err == nil && u.Host == r.Host
}

func (s *Server) apiList(w http.ResponseWriter, r *http.Request) {
	m, err := s.raw.List(r.FormValue("host"), r.FormValue("tool"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(m)
}

func (s *Server) apiPut(w http.ResponseWriter, r *http.Request) {
	var mt time.Time
	if n, err := strconv.ParseInt(r.Header.Get("X-Mtime"), 10, 64); err == nil {
		mt = time.Unix(0, n)
	}
	q := r.URL.Query()
	if err := s.putUpload(q.Get("host"), q.Get("tool"), q.Get("path"), r.Body, mt); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// apiFile は原本1ファイルを返す（元の AI へ戻すときに使う）。
func (s *Server) apiFile(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	p, err := s.raw.Path(q.Get("host"), q.Get("tool"), q.Get("path"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeFile(w, r, p)
}

// apiFind は会話 ID の先頭から会話を探す。
func (s *Server) apiFind(w http.ResponseWriter, r *http.Request) {
	id, err := s.st.FindByExternal(r.URL.Query().Get("id"))
	if err != nil {
		http.Error(w, "見つからない", http.StatusNotFound)
		return
	}
	c, _, err := s.st.Get(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(c)
}

func (s *Server) apiImport(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	res, err := s.ingest(q.Get("host"), q.Get("tool"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"Result": res.String(), "Imported": res.Imported, "Skipped": res.Skipped})
}

func (s *Server) ingest(host, tool string) (ingest.Result, error) {
	return s.ingestProgress(host, tool, nil)
}

func (s *Server) ingestProgress(host, tool string, p ingest.Progress) (ingest.Result, error) {
	dir, err := s.raw.Root(host, tool)
	if err != nil {
		return ingest.Result{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := ingest.RunProgress(s.st, tool, host, dir, p)
	log.Printf("%s/%s: %s %v", host, tool, res, err)
	return res, err
}

func (s *Server) uploadForm(w http.ResponseWriter, r *http.Request) {
	d, err := s.side(r, nil)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	hosts := map[string]bool{}
	cs, _ := s.st.Counts()
	var hs []string
	for _, c := range cs {
		if !hosts[c.Host] {
			hosts[c.Host] = true
			hs = append(hs, c.Host)
		}
	}
	d["Hosts"], d["Page"] = hs, "upload"
	s.render(w, r, "upload.html", d)
}

// uploaded は上げたファイル1つの結果（画面が表示する）。
type uploaded struct {
	Name     string `json:"name"`
	Tool     string `json:"tool,omitempty"`
	Label    string `json:"label,omitempty"`
	Imported int    `json:"imported"`
	Skipped  int    `json:"skipped"`
	Error    string `json:"error,omitempty"`
	Href     string `json:"href,omitempty"`
	Export   bool   `json:"export,omitempty"` // 書き出しの一覧を受け取り、中身を落とし始めた
	Job      string `json:"job,omitempty"`    // 裏で取り込んでいる仕事
}

// manifestUpload は上げたファイルが claude.ai の書き出しの一覧なら、落とし始めて ok を返す。
func (s *Server) manifestUpload(host string, fh *multipart.FileHeader) (string, bool, error) {
	if !strings.HasSuffix(fh.Filename, ".json") || fh.Size > 1<<20 {
		return "", false, nil
	}
	f, err := fh.Open()
	if err != nil {
		return "", false, nil
	}
	defer f.Close()
	b, _ := io.ReadAll(f)
	if !isManifest(b) {
		return "", false, nil
	}
	if !s.uploadAllowed(claudeai.Tool) {
		return "", true, errNotAllowed
	}
	id, err := s.startExport(host, b)
	return id, true, err
}

// upload は画面から上げられたファイルを、中身からツールを見分けて原本に置き、取り込む。
func (s *Server) upload(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	l := lang(r)
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	host := strings.TrimSpace(r.FormValue("host"))
	if host == "" {
		host = "upload"
	}
	var out []uploaded
	for _, fh := range r.MultipartForm.File["files"] {
		u := uploaded{Name: fh.Filename}
		// claude.ai の書き出しの一覧（manifest）なら、サーバーが中身を落として選ばせる
		if id, ok, err := s.manifestUpload(host, fh); ok {
			if err != nil {
				u.Error = err.Error()
			} else {
				u.Tool, u.Label, u.Export = claudeai.Tool, tr(l, "claude.ai の書き出し"), true
				u.Href = "/exports/" + url.PathEscape(host) + "/" + id
			}
			out = append(out, u)
			continue
		}
		tool, _, err := s.store(host, fh)
		if err != nil {
			u.Error = tr(l, err.Error())
		} else {
			// 取り込みは裏で進める（大きなものは数分かかる）。画面は進み具合を見に来る
			u.Tool, u.Label, u.Job = tool, toolLabel(tool), s.startJob(host, tool, r.FormValue("ai") == "1", lang(r))
			u.Href = "/?" + url.Values{"tool": {tool}, "host": {host}}.Encode()
		}
		out = append(out, u)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

func (s *Server) store(host string, fh *multipart.FileHeader) (tool, rel string, err error) {
	f, err := fh.Open()
	if err != nil {
		return "", "", err
	}
	defer f.Close()
	name := path.Base(strings.ReplaceAll(fh.Filename, `\`, "/"))
	// Google Takeout（zip）か、そこから出したマイ アクティビティの HTML なら Gemini の会話だけを置く
	allowGemini := s.uploadAllowed(gemini.Tool)
	if ok, err := s.storeGemini(host, name, f, fh.Size, allowGemini); ok || err != nil {
		if ok && !allowGemini {
			return "", "", errNotAllowed
		}
		return gemini.Tool, "", err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", "", err
	}
	if strings.HasSuffix(name, ".zip") || strings.HasSuffix(name, ".json") {
		tool = detectWeb(name, f, fh.Size)
		rel = "upload/" + time.Now().Format("20060102-150405") + "-" + name
	} else {
		tool, rel = detect(name, f)
	}
	if tool == "" {
		return "", "", errors.New("会話の記録ではない")
	}
	if !s.uploadAllowed(tool) {
		return "", "", errNotAllowed
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", "", err
	}
	var body io.Reader = f
	// 添付ファイルを取り込まない設定なら、埋め込まれた画像などを抜いてから置く（形のある記録だけ）
	if keep := s.uploadAttach(); !push.KeepsAll(keep) && (tool == claudecode.Tool || tool == codex.Tool) {
		t, _, done, err := push.Strip(f, keep, "")
		if err != nil {
			return "", "", err
		}
		defer done()
		body = t
	}
	return tool, rel, s.putUpload(host, tool, rel, body, time.Now())
}

var errNotAllowed = errors.New("取り込まない設定にしている")

// uploadAllowed は設定の「アップロード」で tool を取り込むことにしているか。
func (s *Server) uploadAllowed(tool string) bool {
	return push.Selected(s.st.Setting("upload_tools", "*"), tool)
}

// detect はファイル名と先頭行から、どのツールの記録かと置き場所を決める。
func detect(name string, r io.Reader) (tool, rel string) {
	switch {
	case strings.HasSuffix(name, ".db"), strings.HasSuffix(name, ".db-wal"):
		return antigravity.Tool, name
	case name == "session_index.jsonl":
		return codex.Tool, name
	}
	if !strings.HasSuffix(name, ".jsonl") {
		return "", ""
	}
	first, _ := bufio.NewReader(r).ReadString('\n')
	switch {
	case strings.Contains(first, `"session_meta"`):
		return codex.Tool, "sessions/upload/" + name
	case strings.Contains(first, `"sessionId"`):
		return claudecode.Tool, "upload/" + name
	}
	return "", ""
}

// detectWeb は Web のチャットの書き出し（zip か conversations.json）がどの AI のものかを見分ける。
func detectWeb(name string, f multipart.File, size int64) string {
	var head []byte
	if strings.HasSuffix(name, ".zip") {
		z, err := zip.NewReader(f, size)
		if err != nil {
			return ""
		}
		if head, err = webexport.FromZip(z); err != nil {
			return ""
		}
	} else {
		head, _ = io.ReadAll(io.LimitReader(f, 1<<20))
	}
	if len(head) > 1<<20 {
		head = head[:1<<20]
	}
	switch {
	case chatgpt.Is(head):
		return chatgpt.Tool
	case claudeai.Is(head):
		return claudeai.Tool
	}
	return ""
}

// storeGemini は Takeout の zip から Gemini アプリのマイ アクティビティの HTML だけを取り出して置く
// （zip には写真など他のサービスも入っていて大きいので、丸ごとは置かない）。
func (s *Server) storeGemini(host, name string, f multipart.File, size int64, put bool) (bool, error) {
	stamp := time.Now().Format("20060102-150405")
	if strings.HasSuffix(name, ".html") {
		head := make([]byte, 2<<20)
		n, _ := io.ReadFull(f, head)
		if !gemini.Is(head[:n]) {
			return false, nil
		}
		if !put {
			return true, nil
		}
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return true, err
		}
		return true, s.putUpload(host, gemini.Tool, "takeout/"+stamp+"-"+name, f, time.Now())
	}
	if !strings.HasSuffix(name, ".zip") {
		return false, nil
	}
	z, err := zip.NewReader(f, size)
	if err != nil {
		return false, nil
	}
	found := false
	for i, e := range z.File {
		if !strings.HasSuffix(e.Name, ".html") || e.UncompressedSize64 < 1024 {
			continue
		}
		r, err := e.Open()
		if err != nil {
			continue
		}
		head := make([]byte, 2<<20)
		n, _ := io.ReadFull(r, head)
		r.Close()
		if !gemini.Is(head[:n]) {
			continue
		}
		if !put {
			return true, nil
		}
		r, err = e.Open()
		if err != nil {
			return true, err
		}
		err = s.putUpload(host, gemini.Tool, fmt.Sprintf("takeout/%s-%s-%d.html", stamp, strings.TrimSuffix(name, ".zip"), i), r, e.Modified)
		r.Close()
		if err != nil {
			return true, err
		}
		found = true
	}
	return found, nil
}

// job は裏で進める取り込み1つの様子。
type job struct {
	State    string `json:"state"` // waiting（ほかの取り込みの順番待ち） / running / done / error
	Done     int    `json:"done"`
	Total    int    `json:"total"`
	Imported int    `json:"imported"`
	Skipped  int    `json:"skipped"`
	Error    string `json:"error,omitempty"`
	AI       string `json:"ai,omitempty"` // 取り込んだあと AI で整理している仕事
}

var jobs = struct {
	sync.Mutex
	m map[string]*job
}{m: map[string]*job{}}

func newJobID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Server) startJob(host, tool string, organize bool, l string) string {
	id := newJobID()
	j := &job{State: "waiting"}
	jobs.Lock()
	jobs.m[id] = j
	jobs.Unlock()
	go func() {
		res, err := s.ingestProgress(host, tool, func(done, total int) {
			jobs.Lock()
			j.State, j.Done, j.Total = "running", done, total
			jobs.Unlock()
		})
		// 選んだときだけ、取り込んだ会話を AI で整理する
		var aiJob string
		if c := s.aiConfig(); organize && err == nil && c.Ready() && len(res.Keys) > 0 {
			var ids []int64
			for _, k := range res.Keys {
				if id := s.st.LookupExt(k[0], host, k[1]); id != 0 {
					ids = append(ids, id)
				}
			}
			if len(ids) > 0 {
				aiJob = s.startOrganize(ids, c, l, false)
			}
		}
		jobs.Lock()
		j.Imported, j.Skipped, j.State, j.AI = res.Imported, res.Skipped, "done", aiJob
		if err != nil {
			j.State, j.Error = "error", err.Error()
		}
		jobs.Unlock()
		// 見に来なくなった仕事は1時間で忘れる
		time.AfterFunc(time.Hour, func() { jobs.Lock(); delete(jobs.m, id); jobs.Unlock() })
	}()
	return id
}

func (s *Server) jobState(w http.ResponseWriter, r *http.Request) {
	jobs.Lock()
	j, ok := jobs.m[r.PathValue("id")]
	var v job
	if ok {
		v = *j
	}
	jobs.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
