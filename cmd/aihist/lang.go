package main

import (
	"os"
	"strings"
)

// ja は端末のメッセージを日本語で出すか。設定ファイルの AIHIST_LANG（導入スクリプトが書く）、無ければ LANG などで決める。
var ja = func() bool {
	for _, v := range []string{os.Getenv("AIHIST_LANG"), clientConfig()["AIHIST_LANG"], os.Getenv("LC_ALL"), os.Getenv("LC_MESSAGES"), os.Getenv("LANG")} {
		if v != "" {
			return strings.HasPrefix(strings.ToLower(v), "ja")
		}
	}
	return false
}()

// m は英語の文 en と、それに当てた日本語の文 j のうち、端末の言語のほうを返す。
func m(en, j string) string {
	if ja {
		return j
	}
	return en
}
