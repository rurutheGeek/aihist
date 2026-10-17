package web

import (
	"net/http"
	"slices"
	"strings"

	"github.com/rurutheGeek/aihist/internal/scrub"
)

// 送る前に消す情報（設定の画面に並べる名前）。
var hideLabels = map[string]string{
	scrub.Secret: "トークン・パスワード・秘密鍵", scrub.Email: "メールアドレス", scrub.Phone: "電話番号",
	scrub.Address: "住所", scrub.Card: "クレジットカード番号", scrub.IP: "IP アドレス",
}

func hideOpts(list string) []attachOpt {
	var out []attachOpt
	for _, k := range scrub.Kinds {
		out = append(out, attachOpt{Kind: k, Label: hideLabels[k], On: scrub.On(list, k)})
	}
	return out
}

// hideList は選んだ種類を並びにする（何も無ければ "-"）。
func hideList(on []string) string {
	var l []string
	for _, k := range scrub.Kinds {
		if slices.Contains(on, k) {
			l = append(l, k)
		}
	}
	if len(l) == 0 {
		return "-"
	}
	return strings.Join(l, ",")
}

// hideDefault はすべてのマシンの既定で消す種類。
func (s *Server) hideDefault() string { return s.st.Setting("hide", scrub.Default) }

// uploadHide はアップロードで消す種類。
func (s *Server) uploadHide() string { return s.st.Setting("upload_hide", scrub.Default) }

// hideFor はマシン host で消す種類（マシンで決めていなければ既定）。
func (s *Server) hideFor(host string) string {
	if m, _ := s.st.Machine(host); m.Hide != "" {
		return m.Hide
	}
	return s.hideDefault()
}

// setHide はすべてのマシンの既定で消す種類を保存する。
func (s *Server) setHide(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	r.ParseForm()
	s.st.SetSetting("hide", hideList(r.Form["hide"]))
	done(w, r, "/settings/sending")
}
