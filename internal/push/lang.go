package push

// JA は端末のメッセージを日本語で出すか（コマンドが決める）。
var JA bool

// T は英語の文 en と日本語の文 ja のうち、端末の言語のほうを返す。
func T(en, ja string) string {
	if JA {
		return ja
	}
	return en
}
