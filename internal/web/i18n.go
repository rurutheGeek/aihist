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
	"送る":         "Upload",
	"待機":         "Waiting",
	"送信中":        "Uploading",
	"取り込み中":      "Importing",
	"%d 件取り込み":   "%d imported",
	"%d 件は変更なし":  "%d unchanged",
	"会話の記録ではない":  "Not a conversation log",
	"開く":         "Open",
	"取り込む添付ファイル": "Attachments to import",
	"送る添付ファイル":   "Attachments to send",
	"画像":         "Images",
	"文書（Word・Excel・テキストなど）": "Documents (Word, Excel, text…)",
	"音声":      "Audio",
	"動画":      "Video",
	"その他":     "Other",
	"取り込む種類":  "What to import",
	"取り込む AI": "AIs to import",
	"添付ファイル（画像・PDF など）も取り込む": "Import attachments (images, PDFs…)",
	"取り込まない設定にしている":          "Turned off in Settings → Upload",
	"順番待ち":   "Queued",
	"送れなかった": "Could not upload",
	"落とした zip をここへドロップ":         "Drop the downloaded zips here",
	"zip を待っている":                "Waiting for the zip",
	"%d 件を取り込んだ":                "%d imported",
	"会話を見る":                     "View conversations",
	"1. 各ファイルを claude.ai から落とす": "1. Download each file from claude.ai",
	"2. 落とした zip をここへ":          "2. Drop the downloaded zips here",
	"ダウンロード":                    "Download",
	"この書き出しのファイルではない":           "Not a file from this export",
	"zip ではない":                  "Not a zip file",
	"会話を絞り込んでグループ化":             "Filter and group conversations",
	"サイドバー":                     "Sidebar",
	"サイドバーを非表示":                 "Hide sidebar",
	"サイドバーを表示":                  "Show sidebar",
	"サイドバーのサイズを変更":              "Resize sidebar",
	"検索":                        "Search",
	"ピン留め済み":                    "Pinned",
	"最近の項目":                     "Recents",
	"最近の項目を絞り込んでグループ化":          "Filter and group recents",
	"ステータス":                     "Status",
	"アクティブ":                     "Active",
	"アーカイブ済み":                   "Archived",
	"グループ化の基準":                  "Group by",
	"並び替え":                      "Sort by",
	"最終更新":                      "Last updated",
	"始めた日":                      "Started",
	"なし":                        "None",
	"%sのその他のオプション":              "More options for %s",
	"ピン留め":                      "Pin",
	"ピン留めを外す":                   "Unpin",
	"名前を変更":                     "Rename",
	"アーカイブ":                     "Archive",
	"アーカイブを外す":                  "Unarchive",
	"新しい名前":                     "New name",
	"キーボードショートカット":              "Keyboard shortcuts",
	"サイドバーの表示/非表示":              "Toggle sidebar",
	"会話の行のメニュー":                 "Conversation menu",
	"会話の中":                      "In a conversation",
	"一昨日":                       "2 days ago",
	"%d 日前":                     "%d days ago",
	"サイドバーを閉じる":                 "Close sidebar",
	"サイドバーを開く":                  "Open sidebar",
	"ドラッグで幅を変える（ダブルクリックで元に戻す）": "Drag to resize (double-click to reset)",
	"一般":         "General",
	"送信":         "Sending",
	"トークン":       "Tokens",
	"すべてのマシンの既定": "Default for all machines",
	"まだマシンが無い":   "No machines yet",
	"添付ファイル（画像・PDF など）も送る": "Send attachments (images, PDFs…)",
	"送るタイミングは既定に従う":        "Use the default schedule",
	"このマシンの送るタイミング":        "Schedule for this machine",
	"既定に従う":           "Use the default",
	"情報":              "Info",
	"このマシンを一覧から消す":    "Remove this machine",
	"このマシンを一覧から消す？":   "Remove this machine?",
	"このトークンを削除する？":    "Revoke this token?",
	"スクリプト":           "Scripts",
	"マシンと AI ごと":      "By machine and AI",
	"保存した":            "Saved",
	"保存できなかった":        "Could not save",
	"送るタイミング":         "Schedule",
	"保存":              "Save",
	"未接続":             "Not connected yet",
	"最後に連絡":           "Last seen",
	"次に送る":            "Next send",
	"送る AI":           "AIs to send",
	"まだ報告が無い":         "No report yet",
	"あとで見つかった AI も送る": "Also send AIs found later",
	"添付ファイル":          "Attachments",
	"画像・PDF などの添付ファイルも送る": "Also send images, PDFs and other attachments",
	"全体の設定に従う":            "Use the default",
	"このマシンの設定を消す？":        "Remove this machine's settings?",
	"数分ごと":                "Every few minutes",
	"数時間ごと":               "Every few hours",
	"毎日":                  "Every day",
	"毎週":                  "Every week",
	"毎月":                  "Every month",
	"分ごと":                 "minutes",
	"時間ごと、":               "hours, at minute",
	"分に":                  "",
	"日":                   "Sun",
	"月":                   "Mon",
	"火":                   "Tue",
	"水":                   "Wed",
	"木":                   "Thu",
	"金":                   "Fri",
	"土":                   "Sat",
	"claude.ai の書き出し":     "claude.ai export",
	"中身を選ぶ":               "Choose what to import",
	"ダウンロード中":             "Downloading",
	"取り込み済み":              "Imported",
	"取り込む":                "Import",
	"未対応":                 "Not supported yet",
	"%d 件":                "%d conversations",
	"Claude Code（クラウド）":   "Claude Code (cloud)",
	"メモリ":                 "Memories",
	"フィードバック":             "Feedback",
	"メタデータ":               "Metadata",
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

// relIn は行の吹き出しに出す、いつの会話か（今日 15:04・昨日・3 日前・10月2日）。
func relIn(l string, t time.Time) string {
	if t.IsZero() {
		return ""
	}
	t = t.Local()
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	days := int(today.Sub(time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())).Hours() / 24)
	switch {
	case days <= 0:
		return tr(l, "今日") + " " + t.Format("15:04")
	case days == 1:
		return tr(l, "昨日")
	case days == 2:
		return tr(l, "一昨日")
	case days < 7:
		return trf(l, "%d 日前", days)
	case t.Year() == now.Year():
		if l == "en" {
			return t.Format("Jan 2")
		}
		return t.Format("1月2日")
	}
	if l == "en" {
		return t.Format("Jan 2, 2006")
	}
	return t.Format("2006年1月2日")
}
