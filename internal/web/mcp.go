package web

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/rurutheGeek/aihist/internal/ai"
	"github.com/rurutheGeek/aihist/internal/store"
)

// MCP サーバー（Streamable HTTP の POST だけ。返事は1つの JSON）。
// 利用者の AI が aihist の会話を読み、タグ・要約・ナレッジを書き戻す。トークンはマシンと同じものを使う。

type rpcReq struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

func obj(props map[string]any, req ...string) map[string]any {
	m := map[string]any{"type": "object", "properties": props}
	if len(req) > 0 {
		m["required"] = req
	}
	return m
}

var (
	str  = map[string]any{"type": "string"}
	num  = map[string]any{"type": "integer"}
	strs = map[string]any{"type": "array", "items": str}
)

var mcpTools = []mcpTool{
	{"search", "会話を探す。query は本文・題名・要約から探す。tag・tool で絞れる。", obj(map[string]any{"query": str, "tag": str, "tool": str, "limit": num})},
	{"list_unorganized", "まだタグ・要約を付けていない会話と、付けたあとに続きが足された会話を、新しい順に返す（プログラムや AI が動かした会話は除く）。", obj(map[string]any{"limit": num})},
	{"read_conversation", "会話を読む。長い会話は区切り（page は 1 から）ごとに返すので、pages まで順に読む。", obj(map[string]any{"id": num, "page": num}, "id")},
	{"organize_instructions", "会話を整理するときの決まり（タグの一覧・要約・ナレッジの基準と形）。save_organized の前に読む。", obj(map[string]any{})},
	{"save_organized", "会話を整理した結果を保存する。tags はタグの一覧にある名前だけ。knowledge は下書きに入る（利用者が登録する）。evidence は会話からそのまま写した文で、記録に無ければ捨てられる。",
		obj(map[string]any{"id": num, "model": str, "tags": strs, "summary": str, "knowledge": map[string]any{"type": "array", "items": obj(map[string]any{
			"title": str, "conclusion": str, "environment": str, "symptom": str, "cause": str, "solution": str, "failed": str, "struggle": str, "evidence": strs, "fixed": strs, "score": num,
		}, "title", "conclusion", "solution", "evidence", "fixed", "score")},
			"profile": map[string]any{"type": "array", "items": obj(map[string]any{"text": str, "source": str, "lasting": map[string]any{"type": "boolean"}, "same": num, "replaces": num}, "text", "source", "lasting")}}, "id", "summary")},
	{"get_profile", "ユーザーのプロフィール（どんな人か・経歴・趣味関心・好み・してほしいこと）。答え方をユーザーに合わせるために、作業の最初に読む。", obj(map[string]any{})},
	{"list_profile", "プロフィールの項目を番号つきで返す。kind は about（その人について）か wish（AI への希望）。", obj(map[string]any{})},
	{"set_profile_kind", "プロフィールの項目の種類を変える。kind は about（経歴・趣味・暮らしなど、その人について）か wish（答え方・進め方など、AI にどうしてほしいか）。", obj(map[string]any{"id": num, "kind": str}, "id", "kind")},
	{"list_prompts", "ユーザーが登録したプロンプト（Skill を含む）の名前と説明。", obj(map[string]any{})},
	{"get_prompt", "登録したプロンプトの本文を読む。ユーザーが名前で頼んだら、その本文の指示に従う。", obj(map[string]any{"name": str}, "name")},
	{"save_prompt", "プロンプトを登録する（同じ名前があれば本文と説明を置き換える）。", obj(map[string]any{"name": str, "description": str, "body": str}, "name", "body")},
	{"delete_conversations", "会話を削除する（同じ会話としてまとめた分岐・続きも）。中身は戻せない。消した会話は送り直されても取り込まない。消す前に search で中身を確かめ、ユーザーに確認してから使う。", obj(map[string]any{"ids": map[string]any{"type": "array", "items": num}}, "ids")},
	{"archive_conversations", "会話をアーカイブする（archived が false なら外す）。", obj(map[string]any{"ids": map[string]any{"type": "array", "items": num}, "archived": map[string]any{"type": "boolean"}}, "ids")},
	{"pin_conversations", "会話をピン留めする（pinned が false なら外す）。", obj(map[string]any{"ids": map[string]any{"type": "array", "items": num}, "pinned": map[string]any{"type": "boolean"}}, "ids")},
	{"rename_conversation", "会話の名前を変える（title が空なら元の題名に戻す）。", obj(map[string]any{"id": num, "title": str}, "id")},
	{"set_tags", "会話のタグを tags に置き換える（タグの一覧にある名前だけ）。", obj(map[string]any{"id": num, "tags": strs}, "id", "tags")},
	{"list_tags", "タグの一覧。", obj(map[string]any{})},
	{"list_conversations", "会話の一覧を新しい順に返す（題名・AI・最終更新・タグ・要約）。offset から limit 件（最大 500）。total は全体の件数。全体の傾向を見るときは offset を進めて順に読む。", obj(map[string]any{"offset": num, "limit": num})},
	{"save_tag", "タグを作る（同じ名前があれば説明を変える）。old を渡すと、そのタグの名前を name に変える（付いている会話もそのまま）。description は会話を分けるときの目安になる。", obj(map[string]any{"name": str, "description": str, "old": str}, "name")},
	{"delete_tag", "タグを消す（会話から外れる）。", obj(map[string]any{"name": str}, "name")},
	{"search_knowledge", "登録したナレッジ（苦労して解決した問題の記事）を探す。", obj(map[string]any{"query": str})},
}

func (s *Server) mcp(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req rpcReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<20)).Decode(&req); err != nil {
		writeRPC(w, nil, nil, &rpcErr{-32700, "parse error"})
		return
	}
	if len(req.ID) == 0 { // 知らせ（返事は要らない）
		w.WriteHeader(http.StatusAccepted)
		return
	}
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		json.Unmarshal(req.Params, &p)
		v := p.ProtocolVersion
		if v == "" {
			v = "2025-06-18"
		}
		writeRPC(w, req.ID, map[string]any{
			"protocolVersion": v,
			"capabilities":    map[string]any{"tools": map[string]any{}, "prompts": map[string]any{}},
			"serverInfo":      map[string]any{"name": "aihist", "version": s.Version},
			"instructions":    "aihist は AI との会話の履歴。会話を探して読み、organize_instructions の決まりで整理して save_organized で保存できる。ユーザーが登録したプロンプト（Skill を含む）は list_prompts と get_prompt で読める。長い会話は1件ずつ別のサブエージェントで読むと、文脈を圧迫しない。",
		}, nil)
	case "ping":
		writeRPC(w, req.ID, map[string]any{}, nil)
	case "prompts/list":
		ps, _ := s.st.Prompts()
		out := []map[string]any{}
		for _, p := range ps {
			out = append(out, map[string]any{"name": p.Name, "description": p.Description})
		}
		writeRPC(w, req.ID, map[string]any{"prompts": out}, nil)
	case "prompts/get":
		var p struct {
			Name string `json:"name"`
		}
		json.Unmarshal(req.Params, &p)
		pr, err := s.st.PromptByName(p.Name)
		if err != nil {
			writeRPC(w, req.ID, nil, &rpcErr{-32602, "unknown prompt"})
			return
		}
		writeRPC(w, req.ID, map[string]any{"description": pr.Description, "messages": []map[string]any{
			{"role": "user", "content": map[string]any{"type": "text", "text": pr.Body}}}}, nil)
	case "tools/list":
		writeRPC(w, req.ID, map[string]any{"tools": mcpTools}, nil)
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			writeRPC(w, req.ID, nil, &rpcErr{-32602, "invalid params"})
			return
		}
		out, err := s.mcpCall(r, p.Name, p.Arguments)
		res := map[string]any{"content": []map[string]any{{"type": "text", "text": out}}}
		if err != nil {
			res = map[string]any{"content": []map[string]any{{"type": "text", "text": err.Error()}}, "isError": true}
		}
		writeRPC(w, req.ID, res, nil)
	default:
		writeRPC(w, req.ID, nil, &rpcErr{-32601, "method not found"})
	}
}

func writeRPC(w http.ResponseWriter, id json.RawMessage, result any, e *rpcErr) {
	w.Header().Set("Content-Type", "application/json")
	m := map[string]any{"jsonrpc": "2.0", "id": id}
	if e != nil {
		m["error"] = e
	} else {
		m["result"] = result
	}
	json.NewEncoder(w).Encode(m)
}

func jsonText(v any) string {
	b, _ := json.MarshalIndent(v, "", " ")
	return string(b)
}

type convBrief struct {
	ID      int64    `json:"id"`
	Title   string   `json:"title"`
	Tool    string   `json:"tool"`
	Updated string   `json:"updated"`
	Tags    []string `json:"tags,omitempty"`
	Summary string   `json:"summary,omitempty"`
	Snippet string   `json:"snippet,omitempty"`
}

func brief(rows []store.ConvRow) []convBrief {
	out := []convBrief{}
	for _, c := range rows {
		out = append(out, convBrief{ID: c.ID, Title: strings.Join(strings.Fields(c.Title), " "), Tool: c.Tool, Updated: c.UpdatedAt.Format("2006-01-02 15:04"),
			Tags: c.Tags, Summary: c.Summary, Snippet: strings.NewReplacer("\x02", "", "\x03", "").Replace(c.Snippet)})
	}
	return out
}

func (s *Server) mcpCall(r *http.Request, name string, args json.RawMessage) (string, error) {
	var a struct {
		Query     string         `json:"query"`
		Tag       string         `json:"tag"`
		Tool      string         `json:"tool"`
		Limit     int            `json:"limit"`
		ID        int64          `json:"id"`
		Page      int            `json:"page"`
		Model     string         `json:"model"`
		Tags      []string       `json:"tags"`
		Summary   string         `json:"summary"`
		Knowledge []ai.Knowledge `json:"knowledge"`
		Profile   []ai.Trait     `json:"profile"`
		IDs       []int64        `json:"ids"`
		Archived  *bool          `json:"archived"`
		Pinned    *bool          `json:"pinned"`
		Title     *string        `json:"title"`
		Offset    int            `json:"offset"`
		Name      string         `json:"name"`
		Desc      string         `json:"description"`
		Old       string         `json:"old"`
		Kind      string         `json:"kind"`
		Body      string         `json:"body"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return "", fmt.Errorf("引数を読めない: %v", err)
		}
	}
	if name == "list_conversations" {
		if a.Limit <= 0 || a.Limit > 500 {
			a.Limit = 100
		}
	} else if a.Limit <= 0 || a.Limit > 100 {
		a.Limit = 20
	}
	switch name {
	case "search":
		f := store.Filter{Query: a.Query, Status: "all", Limit: a.Limit}
		if a.Tag != "" {
			f.Tags = []string{a.Tag}
		}
		if a.Tool != "" {
			f.Tools = []string{a.Tool}
		}
		rows, _, err := s.st.Browse(f)
		if err != nil {
			return "", err
		}
		return jsonText(brief(rows)), nil
	case "list_unorganized":
		rows, _, err := s.st.Browse(store.Filter{Status: "all", Limit: 100000})
		if err != nil {
			return "", err
		}
		var out []store.ConvRow
		for _, c := range rows {
			if !c.Auto && (c.Summary == "" || s.st.Stale(c.ID)) {
				out = append(out, c)
				if len(out) == a.Limit {
					break
				}
			}
		}
		return jsonText(brief(out)), nil
	case "read_conversation":
		c, msgs, err := s.st.Get(a.ID)
		if err != nil {
			return "", fmt.Errorf("会話 %d が無い", a.ID)
		}
		pages := ai.Pages(msgs, ai.PageSize)
		if a.Page < 1 {
			a.Page = 1
		}
		if a.Page > len(pages) {
			return "", fmt.Errorf("区切りは %d まで", len(pages))
		}
		return fmt.Sprintf("# %s\n(id %d · %s · %s の会話 · 区切り %d / %d)\n\n%s", c.Title, c.ID, toolLabel(c.Tool), c.StartedAt.Local().Format("2006-01-02"), a.Page, len(pages), pages[a.Page-1]), nil
	case "organize_instructions":
		return ai.Instructions(s.aiTags(), "", s.profileReq()) + "\n\n結果は save_organized に渡す（JSON の各項目をそのまま引数にする）。", nil
	case "save_organized":
		c, msgs, err := s.st.Get(a.ID)
		if err != nil {
			return "", fmt.Errorf("会話 %d が無い", a.ID)
		}
		pages := ai.Pages(msgs, ai.PageSize)
		res := ai.Result{Tags: a.Tags, Summary: a.Summary, Knowledge: a.Knowledge, Profile: a.Profile}.Clean(s.aiTags(), strings.Join(pages, "\n"))
		model := "mcp"
		if a.Model != "" {
			model = "mcp: " + a.Model
		}
		n, err := s.saveResult(c, pages, res, model, ai.LangOf(a.Summary))
		if err != nil {
			return "", err
		}
		dropped := len(a.Knowledge) - n
		msg := fmt.Sprintf("保存した（タグ %d・ナレッジの下書き %d）", len(res.Tags), n)
		if dropped > 0 {
			msg += fmt.Sprintf("。ナレッジ %d 件は決まり（score %d 以上・evidence と fixed が会話にそのままあること・conclusion と solution）に合わず捨てた", dropped, ai.MinScore)
		}
		return msg, nil
	case "get_profile":
		ts, err := s.st.Profile()
		if err != nil {
			return "", err
		}
		return profileMarkdown("ja", ts), nil
	case "list_profile":
		ts, err := s.st.Profile()
		if err != nil {
			return "", err
		}
		type item struct {
			ID   int64  `json:"id"`
			Kind string `json:"kind"`
			Text string `json:"text"`
		}
		out := []item{}
		for _, t := range ts {
			k := "about"
			if t.Kind == store.KindWish {
				k = "wish"
			}
			out = append(out, item{t.ID, k, t.Text})
		}
		return jsonText(out), nil
	case "set_profile_kind":
		if a.Kind != "about" && a.Kind != "wish" {
			return "", fmt.Errorf("kind は about か wish")
		}
		if err := s.st.SetTraitKind(a.ID, traitKind(a.Kind)); err != nil {
			return "", err
		}
		return "変えた", nil
	case "list_prompts":
		ps, err := s.st.Prompts()
		if err != nil {
			return "", err
		}
		type pb struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		out := []pb{}
		for _, p := range ps {
			out = append(out, pb{p.Name, p.Description})
		}
		return jsonText(out), nil
	case "get_prompt":
		p, err := s.st.PromptByName(a.Name)
		if err != nil {
			return "", fmt.Errorf("プロンプト「%s」は無い", a.Name)
		}
		return p.Body, nil
	case "save_prompt":
		n := strings.TrimSpace(a.Name)
		if n == "" || strings.TrimSpace(a.Body) == "" {
			return "", fmt.Errorf("name と body が要る")
		}
		p, _ := s.st.PromptByName(n)
		p.Name, p.Description, p.Body = n, strings.TrimSpace(a.Desc), a.Body
		if _, err := s.st.SavePrompt(p); err != nil {
			return "", err
		}
		return "プロンプト「" + n + "」を保存した", nil
	case "delete_conversations":
		if len(a.IDs) == 0 {
			return "", fmt.Errorf("ids が要る")
		}
		n, err := s.st.Delete(a.IDs...)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%d 件の会話を削除した", n), nil
	case "archive_conversations", "pin_conversations":
		if len(a.IDs) == 0 {
			return "", fmt.Errorf("ids が要る")
		}
		on := true
		var pin, arc *bool
		if name == "archive_conversations" {
			if a.Archived != nil {
				on = *a.Archived
			}
			arc = &on
		} else {
			if a.Pinned != nil {
				on = *a.Pinned
			}
			pin = &on
		}
		n := 0
		for _, id := range a.IDs {
			if s.st.SetMeta(id, nil, pin, arc) == nil {
				n++
			}
		}
		return fmt.Sprintf("%d 件を変えた", n), nil
	case "rename_conversation":
		t := ""
		if a.Title != nil {
			t = strings.TrimSpace(*a.Title)
		}
		if err := s.st.SetMeta(a.ID, &t, nil, nil); err != nil {
			return "", fmt.Errorf("会話 %d が無い", a.ID)
		}
		return "名前を変えた", nil
	case "set_tags":
		if err := s.st.SetConvTags(a.ID, a.Tags); err != nil {
			return "", fmt.Errorf("会話 %d が無い", a.ID)
		}
		c, _, _ := s.st.Get(a.ID)
		if c == nil {
			return "", nil
		}
		return "タグ: " + strings.Join(c.Tags, ", "), nil
	case "list_tags":
		ts, err := s.st.Tags()
		if err != nil {
			return "", err
		}
		return jsonText(ts), nil
	case "list_conversations":
		rows, _, err := s.st.Browse(store.Filter{Status: "all", Limit: 100000})
		if err != nil {
			return "", err
		}
		total := len(rows)
		rows = rows[min(max(a.Offset, 0), total):]
		rows = rows[:min(a.Limit, len(rows))]
		return jsonText(map[string]any{"total": total, "offset": a.Offset, "conversations": brief(rows)}), nil
	case "save_tag":
		n := strings.TrimSpace(a.Name)
		if n == "" {
			return "", fmt.Errorf("name が要る")
		}
		if a.Old != "" && a.Old != n {
			if err := s.st.RenameTag(a.Old, n); err != nil {
				return "", err
			}
		}
		if err := s.st.SetTag(n, strings.TrimSpace(a.Desc)); err != nil {
			return "", err
		}
		return "タグ「" + n + "」を保存した", nil
	case "delete_tag":
		if err := s.st.DeleteTag(a.Name); err != nil {
			return "", err
		}
		return "タグ「" + a.Name + "」を消した", nil
	case "search_knowledge":
		ks, err := s.st.KnowledgeList("registered", a.Query)
		if err != nil {
			return "", err
		}
		type kb struct {
			ID    int64  `json:"id"`
			Title string `json:"title"`
			Body  string `json:"body"`
		}
		out := []kb{}
		for _, k := range ks {
			out = append(out, kb{k.ID, k.Title, k.Body})
			if len(out) == a.Limit {
				break
			}
		}
		return jsonText(out), nil
	}
	return "", fmt.Errorf("知らない道具 %s", name)
}
