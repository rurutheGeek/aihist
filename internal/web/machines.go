package web

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/rurutheGeek/aihist/internal/sched"
)

type toolOpt struct {
	Tool, Label string
	Files       int
	Bytes       int64
	On          bool
}

type machineView struct {
	Host, Version string
	SeenAt        time.Time
	Tools         []toolOpt
	AllTools      bool
	Attachments   bool
	Follow        bool // 送るタイミングは全体の設定に従う
	Schedule      string
	Cleanup       int
	Next          []string
}

// loc は予定の時刻の地域（設定の画面で保存したブラウザの地域、無ければサーバーの地域）。
func (s *Server) loc() *time.Location {
	if l, err := time.LoadLocation(s.st.Setting("tz", "")); err == nil && s.st.Setting("tz", "") != "" {
		return l
	}
	return time.Local
}

// nextRuns は予定の次の n 回を、画面の言語で並べる。
func (s *Server) nextRuns(l, expr string, n int) []string {
	spec, err := sched.Parse(expr)
	if err != nil {
		return nil
	}
	var out []string
	t := time.Now().In(s.loc())
	wd := []string{"日", "月", "火", "水", "木", "金", "土"}
	for range n {
		if t = spec.Next(t); t.IsZero() {
			break
		}
		if l == "en" {
			out = append(out, t.Format("Mon Jan 2 15:04"))
		} else {
			out = append(out, t.Format("1月2日")+"("+wd[t.Weekday()]+") "+t.Format("15:04"))
		}
	}
	return out
}

func (s *Server) machineViews(l string) []machineView {
	ms, _ := s.st.Machines()
	global := s.st.Setting("schedule", DefaultSchedule)
	var out []machineView
	for _, m := range ms {
		v := machineView{Host: m.Host, Version: m.Version, SeenAt: m.SeenAt, AllTools: strings.HasPrefix(m.Tools, "*"),
			Attachments: m.Attachments, Follow: m.Schedule == "", Schedule: m.Schedule, Cleanup: m.Cleanup}
		chosen := strings.Split(m.Tools, ",")
		for _, d := range m.Detected {
			v.Tools = append(v.Tools, toolOpt{Tool: d.Tool, Label: toolLabel(d.Tool), Files: d.Files, Bytes: d.Bytes,
				On: v.AllTools && !slices.Contains(chosen, "-"+d.Tool) || slices.Contains(chosen, d.Tool)})
		}
		if v.Follow {
			v.Schedule = global
		}
		v.Next = s.nextRuns(l, v.Schedule, 1)
		out = append(out, v)
	}
	return out
}

// setSchedule は全体の送るタイミングを保存する。
func (s *Server) setSchedule(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if _, err := sched.Parse(r.FormValue("schedule")); err == nil {
		s.st.SetSetting("schedule", r.FormValue("schedule"))
	}
	if tz := r.FormValue("tz"); tz != "" {
		if _, err := time.LoadLocation(tz); err == nil {
			s.st.SetSetting("tz", tz)
		}
	}
	done(w, r, "/settings/sending")
}

// setMachine はマシン1台の設定を保存する。
func (s *Server) setMachine(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	m, _ := s.st.Machine(r.PathValue("host"))
	r.ParseForm()
	// 「あとで見つかった AI も送る」なら、外したものだけを除いた全部（*,-tool）
	on := r.Form["tool"]
	if r.FormValue("all_tools") == "1" {
		l := []string{"*"}
		for _, d := range m.Detected {
			if !slices.Contains(on, d.Tool) {
				l = append(l, "-"+d.Tool)
			}
		}
		m.Tools = strings.Join(l, ",")
	} else if m.Tools = strings.Join(on, ","); m.Tools == "" {
		m.Tools = "-" // 何も送らない
	}
	m.Attachments = r.FormValue("attachments") == "1"
	m.Schedule = ""
	if r.FormValue("follow") != "1" {
		if _, err := sched.Parse(r.FormValue("schedule")); err == nil {
			m.Schedule = r.FormValue("schedule")
		}
	}
	if c, err := strconv.Atoi(r.FormValue("cleanup")); err == nil && c >= -1 {
		m.Cleanup = c
	}
	s.st.SetMachine(m)
	done(w, r, "/settings/machines/"+m.Host)
}

func (s *Server) deleteMachine(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	s.st.DeleteMachine(r.PathValue("host"))
	http.Redirect(w, r, "/settings/machines", http.StatusSeeOther)
}

// preview は組み立て中の予定の、次に送る日時（画面がその場で表示する）。
func (s *Server) preview(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.nextRuns(lang(r), r.URL.Query().Get("schedule"), 3))
}
