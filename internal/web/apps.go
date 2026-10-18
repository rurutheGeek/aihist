package web

import (
	"context"
	"net/http"
	"strconv"
	"strings"
)

// connApp は設定の「連携」に並べるアプリ。Steps はそのアプリでの手順、Code は貼るもの。
type connApp struct {
	Key, Name string
	Steps     []string
	Codes     []connCode
}

type connCode struct{ Label, Text string }

// connApps は連携できるアプリと、トークン tok で接続する手順（tok が空なら一覧に出すだけ）。
func connApps(l, url, tok string) []connApp {
	auth := "Bearer " + tok
	j := func(top, urlKey, extra string) string {
		return "{\n  \"" + top + "\": {\n    \"aihist\": {\n" + extra +
			"      \"" + urlKey + "\": \"" + url + "\",\n      \"headers\": { \"Authorization\": \"" + auth + "\" }\n    }\n  }\n}"
	}
	add := func(top string) string { return trf(l, "%s に aihist を足して保存する", top) }
	return []connApp{
		{Key: "claude-code", Name: "Claude Code",
			Steps: []string{tr(l, "ターミナルで実行する")},
			Codes: []connCode{{Text: `claude mcp add --transport http aihist ` + url + ` --header "Authorization: ` + auth + `"`}}},
		{Key: "antigravity", Name: "Antigravity",
			Steps: []string{tr(l, "エージェントの欄の … → MCP Servers → Manage MCP Servers → View raw config を開く"), add("mcpServers")},
			Codes: []connCode{{Text: j("mcpServers", "serverUrl", "")}}},
		{Key: "cursor", Name: "Cursor",
			Steps: []string{tr(l, "~/.cursor/mcp.json を開く"), add("mcpServers")},
			Codes: []connCode{{Text: j("mcpServers", "url", "")}}},
		{Key: "vscode", Name: "VS Code",
			Steps: []string{tr(l, "コマンド パレットで MCP: Open User Configuration を実行する"), add("servers")},
			Codes: []connCode{{Text: j("servers", "url", "      \"type\": \"http\",\n")}}},
		{Key: "gemini-cli", Name: "Gemini CLI",
			Steps: []string{tr(l, "~/.gemini/settings.json を開く"), add("mcpServers")},
			Codes: []connCode{{Text: j("mcpServers", "httpUrl", "")}}},
		{Key: "other", Name: tr(l, "その他のアプリ"),
			Steps: []string{tr(l, "アプリの MCP サーバーの設定に、次の URL とヘッダーを入れる")},
			Codes: []connCode{{Label: "URL", Text: url}, {Label: tr(l, "ヘッダー"), Text: "Authorization: " + auth}}},
	}
}

type newAppKey struct{}

// connect はアプリの名前でトークンを作り、そのアプリでの手順を1度だけ出す。
func (s *Server) connect(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var app *connApp
	for _, a := range connApps(lang(r), "", "") {
		if a.Key == r.FormValue("app") {
			app = &a
		}
	}
	if app == nil {
		http.Redirect(w, r, "/settings/apps", http.StatusSeeOther)
		return
	}
	name := app.Name
	if n := strings.TrimSpace(r.FormValue("name")); app.Key == "other" && n != "" {
		name = n
	}
	tok, err := s.st.CreateToken(name)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	r = r.WithContext(context.WithValue(r.Context(), newAppKey{}, app.Key))
	s.renderSettings(w, r, name, tok)
}

func (s *Server) disconnect(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if id, err := strconv.ParseInt(r.PathValue("id"), 10, 64); err == nil {
		s.st.DeleteToken(id)
	}
	http.Redirect(w, r, "/settings/apps", http.StatusSeeOther)
}
