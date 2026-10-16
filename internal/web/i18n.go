package web

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// 画面の文言は日本語を鍵にし、英語はこの表から引く。表に無いもの（固有名など）はそのまま出す。
var english = map[string]string{
	"会話を検索":        "Search conversations",
	"絞り込み":         "Filter",
	"クリア":          "Clear",
	"さらに表示":        "Show more",
	"閉じる":          "Show less",
	"期間":           "Period",
	"すべて":          "All",
	"まとめ方":         "Group by",
	"日付":           "Date",
	"プロジェクト":       "Project",
	"モデル":          "Model",
	"マシン":          "Machine",
	"今日":           "Today",
	"昨日":           "Yesterday",
	"過去7日":         "Previous 7 days",
	"過去30日":        "Previous 30 days",
	"日付なし":         "No date",
	"7日":           "7 days",
	"30日":          "30 days",
	"90日":          "90 days",
	"1年":           "1 year",
	"見つからない":       "Nothing found",
	"サブエージェント":     "Subagent",
	"アップロード":       "Upload",
	"設定":           "Settings",
	"一覧":           "Conversations",
	"Markdown を保存": "Save as Markdown",
	"親の会話":         "Parent",
	"版":            "Versions",
	"続き":           "Continued",
	"思考":           "Thinking",
	"入力":           "Input",
	"結果":           "Result",
	"エラー":          "Error",
	"%d 回の操作":      "%d steps",
	"前の発言":         "Previous message",
	"次の発言":         "Next message",
	"最初へ":          "Top",
	"最新へ":          "Latest",
	"元の AI で続ける":   "Continue in the original AI",
	"%sで続ける":       "Continue in %s",
	"別のAIへ渡す":      "Copy for another AI",
	"ツール除外":        "No tools",
	"コピー":          "Copy",
	"コピーした":        "Copied",
	// 設定
	"マシンから送る": "Send from machines",
	"マシンを追加":  "Add machine",
	"マシン名":    "Machine name",
	"追加":      "Add",
	"削除":      "Revoke",
	"作成":      "Created",
	"最後に使った":  "Last used",
	"未使用":     "Never",
	"環境変数":    "Environment",
	"入れる":     "Install",
	"消す":      "Uninstall",
	"このトークンは今しか表示されない": "This token is shown only once",
	"容量":     "Storage",
	"会話":     "Conversations",
	"原本":     "Originals",
	"データベース": "Database",
	"合計":     "Total",
	"言語":     "Language",
	"自動":     "Auto",
	// アップロード
	"ファイルをドロップ、またはクリックして選ぶ": "Drop files here or click to choose",
	"送る":        "Upload",
	"待機":        "Waiting",
	"送信中":       "Uploading",
	"取り込み中":     "Importing",
	"%d 件取り込み":  "%d imported",
	"%d 件は変更なし": "%d unchanged",
	"会話の記録ではない": "Not a conversation log",
	"開く":        "Open",
}

// lang は cookie の lang、無ければブラウザの言語で決める。
func lang(r *http.Request) string {
	if c, err := r.Cookie("lang"); err == nil && (c.Value == "ja" || c.Value == "en") {
		return c.Value
	}
	for _, l := range strings.Split(r.Header.Get("Accept-Language"), ",") {
		l = strings.ToLower(strings.TrimSpace(l))
		if strings.HasPrefix(l, "ja") {
			return "ja"
		}
		if strings.HasPrefix(l, "en") {
			return "en"
		}
	}
	return "en"
}

func tr(l, s string) string {
	if l == "en" {
		if v, ok := english[s]; ok {
			return v
		}
	}
	return s
}

func trf(l, s string, a ...any) string { return fmt.Sprintf(tr(l, s), a...) }

// dayIn は一覧の日付の見出し。
func dayIn(l string, t time.Time) string {
	if t.IsZero() {
		return tr(l, "日付なし")
	}
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	switch {
	case !t.Before(today):
		return tr(l, "今日")
	case !t.Before(today.AddDate(0, 0, -1)):
		return tr(l, "昨日")
	case !t.Before(today.AddDate(0, 0, -7)):
		return tr(l, "過去7日")
	case !t.Before(today.AddDate(0, 0, -30)):
		return tr(l, "過去30日")
	case t.Year() == now.Year():
		if l == "en" {
			return t.Format("January")
		}
		return t.Format("1月")
	}
	if l == "en" {
		return t.Format("January 2006")
	}
	return t.Format("2006年1月")
}
