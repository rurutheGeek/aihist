package web

import (
	"html/template"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/rurutheGeek/aihist/internal/store"
)

type item struct {
	Value, Label, Title string
	Color               template.CSS
	Count               int
	On                  bool
}

type section struct {
	Dim, Label string
	Items      []item
	On         int
}

type pill struct {
	Label, Href string
	Color       template.CSS
}

type rowGroup struct {
	Label, Title string
	Rows         []store.ConvRow
}

var periods = []struct{ Value, Label string }{
	{"1d", "今日"}, {"7d", "7日"}, {"30d", "30日"}, {"90d", "90日"}, {"365d", "1年"},
}

var dimLabels = map[string]string{
	store.DimTool: "AI", store.DimModel: "モデル", store.DimHost: "マシン", store.DimProject: "プロジェクト",
}

// side は左の会話一覧と絞り込み（どの画面にも出す）。
func (s *Server) side(r *http.Request, q url.Values) (map[string]any, error) {
	l := lang(r)
	f := store.Filter{
		Query: q.Get("q"), Tools: q["tool"], Hosts: q["host"], Models: q["model"], Projects: q["project"], Limit: 300,
	}
	period := q.Get("since")
	for _, p := range periods {
		if p.Value == period {
			d := map[string]int{"1d": 0, "7d": 6, "30d": 29, "90d": 89, "365d": 364}[p.Value]
			y, m, dd := time.Now().Date()
			f.Since = time.Date(y, m, dd-d, 0, 0, 0, 0, time.Local)
		}
	}
	if f.Since.IsZero() {
		period = ""
	}
	byProject := q.Get("by") == "project"
	rows, facets, err := s.st.Browse(f)
	if err != nil {
		return nil, err
	}

	var secs []section
	var pills []pill
	on := 0
	for _, d := range store.Dims {
		want := f.Values(d)
		sec := section{Dim: d, Label: dimLabels[d]}
		seen := map[string]bool{}
		add := func(v string, n int) {
			it := item{Value: v, Label: v, Count: n, On: slices.Contains(want, v)}
			switch d {
			case store.DimTool:
				it.Label, it.Color = toolLabel(v), toolColor(v)
			case store.DimProject:
				it.Label, it.Title = base(v), v
			}
			seen[v] = true
			sec.Items = append(sec.Items, it)
			if it.On {
				sec.On++
				pills = append(pills, pill{Label: it.Label, Color: it.Color, Href: "/" + query(q, d, v)})
			}
		}
		for _, fc := range facets[d] {
			add(fc.Name, fc.Count)
		}
		for _, v := range want {
			if !seen[v] {
				add(v, 0)
			}
		}
		on += sec.On
		if len(sec.Items) > 1 || sec.On > 0 {
			secs = append(secs, sec)
		}
	}
	var pers []item
	for _, p := range periods {
		pers = append(pers, item{Value: p.Value, Label: p.Label, On: p.Value == period})
		if p.Value == period {
			on++
			pills = append(pills, pill{Label: tr(l, p.Label), Href: "/" + query(q, "since", period)})
		}
	}

	var groups []rowGroup
	idx := map[string]int{}
	for _, c := range rows {
		key, label, title := dayIn(l, c.UpdatedAt.Local()), dayIn(l, c.UpdatedAt.Local()), ""
		if byProject {
			key, label, title = c.Cwd, base(c.Cwd), c.Cwd
			if key == "" {
				label = "—"
			}
		}
		i, ok := idx[key]
		if !ok {
			i = len(groups)
			idx[key] = i
			groups = append(groups, rowGroup{Label: label, Title: title})
		}
		groups[i].Rows = append(groups[i].Rows, c)
	}

	keep := query(q, "", "")
	return map[string]any{
		"Filter": f, "Rows": rows, "Groups": groups, "Sections": secs, "Periods": pers, "Pills": pills,
		"On": on, "ByProject": byProject, "Tools": facets[store.DimTool], "Keep": keep,
		"Clear": "/" + query(url.Values{"q": {f.Query}, "by": q["by"]}, "", ""),
	}, nil
}

// query は今の絞り込みから dim=v を1つ外したクエリ（会話を開いても絞り込みを保つ）。
func query(q url.Values, dim, v string) string {
	out := url.Values{}
	for _, k := range []string{"q", "tool", "model", "host", "project", "since", "by"} {
		for _, x := range q[k] {
			if x != "" && (k != dim || x != v) {
				out.Add(k, x)
			}
		}
	}
	if len(out) == 0 {
		return ""
	}
	return "?" + out.Encode()
}
