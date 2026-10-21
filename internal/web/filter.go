package web

import (
	"html/template"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/rurutheGeek/aihist/internal/store"
)

// 左の会話一覧は claude.ai と同じく「ピン留め済み」と「最近の項目」に分け、
// 「最近の項目」の横のボタンから出る小さなメニューで絞り込む（各項目は子メニュー）。

type item struct {
	Value, Label, Title, Href string
	Color                     template.CSS
	Count                     int
	On                        bool
}

type section struct {
	Key, Label, Value string // Value は今の選択の表示（「すべて」・「2 件」など）
	Items             []item
	Multi             bool
}

type pill struct {
	Label, Href string
	Color       template.CSS
}

type rowGroup struct {
	Label, Title string
	Rows         []store.ConvRow
}

type choice struct{ Value, Label string }

var (
	periods  = []choice{{"", "すべて"}, {"1d", "今日"}, {"7d", "7日"}, {"30d", "30日"}, {"90d", "90日"}, {"365d", "1年"}}
	statuses = []choice{{"", "アクティブ"}, {"archived", "アーカイブ済み"}, {"auto", "自動実行"}, {"all", "すべて"}}
	groupBys = []choice{{"", "日付"}, {"project", "プロジェクト"}, {"tool", "AI"}, {"none", "なし"}}
	sorts    = []choice{{"", "最終更新"}, {"started", "始めた日"}}
)

var dimLabels = map[string]string{
	store.DimTool: "AI", store.DimTag: "タグ", store.DimModel: "モデル", store.DimHost: "マシン", store.DimProject: "プロジェクト",
}

// keys は絞り込みのクエリに使う名前（会話を開いても保つ）。
var keys = []string{"q", "tool", "tag", "model", "host", "project", "since", "status", "by", "sort"}

// side は左の会話一覧と絞り込み（どの画面にも出す）。
func (s *Server) side(r *http.Request, q url.Values) (map[string]any, error) {
	l := lang(r)
	f := store.Filter{
		Query: q.Get("q"), Tools: q["tool"], Hosts: q["host"], Models: q["model"], Projects: q["project"], Tags: q["tag"],
		Status: q.Get("status"), Sort: q.Get("sort"), Limit: 300,
	}
	period := q.Get("since")
	if d, ok := map[string]int{"1d": 0, "7d": 6, "30d": 29, "90d": 89, "365d": 364}[period]; ok {
		y, m, dd := time.Now().Date()
		f.Since = time.Date(y, m, dd-d, 0, 0, 0, 0, time.Local)
	} else {
		period = ""
	}
	rows, facets, err := s.st.Browse(f)
	if err != nil {
		return nil, err
	}

	var menu []section
	var pills []pill
	for _, d := range store.Dims {
		want := f.Values(d)
		sec := section{Key: d, Label: dimLabels[d], Multi: true, Value: tr(l, "すべて")}
		seen := map[string]bool{}
		add := func(v string, n int) {
			it := item{Value: v, Label: v, Count: n, On: slices.Contains(want, v), Href: "?" + toggle(q, d, v)}
			switch d {
			case store.DimTool:
				it.Label, it.Color = toolLabel(v), toolColor(v)
			case store.DimProject:
				it.Label, it.Title = base(v), v
			}
			seen[v] = true
			sec.Items = append(sec.Items, it)
			if it.On {
				pills = append(pills, pill{Label: it.Label, Color: it.Color, Href: "?" + toggle(q, d, v)})
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
		if len(want) == 1 {
			sec.Value = sec.Items[slices.IndexFunc(sec.Items, func(i item) bool { return i.On })].Label
		} else if len(want) > 1 {
			sec.Value = trf(l, "%d 件", len(want))
		}
		if len(sec.Items) > 0 {
			menu = append(menu, sec)
		}
	}
	single := func(key, label string, cs []choice, cur string) {
		sec := section{Key: key, Label: label}
		for _, c := range cs {
			it := item{Value: c.Value, Label: tr(l, c.Label), On: c.Value == cur, Href: "?" + set(q, key, c.Value)}
			if it.On {
				sec.Value = it.Label
				if c.Value != "" {
					pills = append(pills, pill{Label: it.Label, Href: "?" + set(q, key, "")})
				}
			}
			sec.Items = append(sec.Items, it)
		}
		menu = append(menu, sec)
	}
	single("since", "期間", periods, period)
	single("status", "ステータス", statuses, f.Status)
	single("by", "グループ化の基準", groupBys, q.Get("by"))
	single("sort", "並び替え", sorts, f.Sort)

	// ピン留めは上に分けて並べる
	var pinned, recent []store.ConvRow
	for _, c := range rows {
		if c.Pinned {
			pinned = append(pinned, c)
		} else {
			recent = append(recent, c)
		}
	}
	var groups []rowGroup
	idx := map[string]int{}
	for _, c := range recent {
		var key, label, title string
		switch q.Get("by") {
		case "project":
			key, label, title = c.Cwd, base(c.Cwd), c.Cwd
			if key == "" {
				label = "—"
			}
		case "tool":
			key, label = c.Tool, toolLabel(c.Tool)
		case "none":
		default:
			key = dayIn(l, c.UpdatedAt.Local())
			if f.Sort == "started" {
				key = dayIn(l, c.StartedAt.Local())
			}
			label = key
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
	clear := url.Values{}
	if v := q.Get("q"); v != "" {
		clear.Set("q", v)
	}
	return map[string]any{
		"Filter": f, "Rows": rows, "Pinned": pinned, "Groups": groups, "Menu": menu, "Pills": pills,
		"Tools": facets[store.DimTool], "Keep": keep, "Clear": "?" + clear.Encode(),
		"AI": s.aiConfig().Ready(), "AIModel": s.aiConfig().Model,
	}, nil
}

// toggle は複数選べる項目 key の値 v を、付いていれば外し、無ければ足したクエリ。
func toggle(q url.Values, key, v string) string {
	out := pick(q)
	l := out[key]
	if i := slices.Index(l, v); i >= 0 {
		out[key] = slices.Delete(slices.Clone(l), i, i+1)
	} else {
		out[key] = append(slices.Clone(l), v)
	}
	return out.Encode()
}

// set は1つだけ選ぶ項目 key を v にしたクエリ（空なら外す）。
func set(q url.Values, key, v string) string {
	out := pick(q)
	if v == "" {
		delete(out, key)
	} else {
		out[key] = []string{v}
	}
	return out.Encode()
}

func pick(q url.Values) url.Values {
	out := url.Values{}
	for _, k := range keys {
		for _, x := range q[k] {
			if x != "" {
				out.Add(k, x)
			}
		}
	}
	return out
}

// query は今の絞り込みから dim=v を1つ外したクエリ（会話を開いても絞り込みを保つ）。
func query(q url.Values, dim, v string) string {
	out := url.Values{}
	for _, k := range keys {
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
