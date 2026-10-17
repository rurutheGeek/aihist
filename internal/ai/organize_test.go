package ai

import "testing"

func TestProven(t *testing.T) {
	text := "$ grep -iE 'out of memory|hung task|oom' /var/log/syslog\n(なし)\nbash: line 4: echo: write error: Invalid argument\n"
	if !proven([]string{"grep -iE 'out of memory|hung task|oom' 等で原因を探したがゼロ", "「bash: line 4: echo: write error: Invalid argument」"}, text) {
		t.Error("引用のあとに言葉を足したものも数える")
	}
	if proven([]string{"ログに何も無かった", "bash: line 4: echo: write error: Invalid argument"}, text) {
		t.Error("記録に無い言い換えは数えない")
	}
}

func TestCleanNeedsFix(t *testing.T) {
	text := "error: boom\nerror: boom again\nok: deployed"
	k := Knowledge{Title: "x", Conclusion: "c", Solution: "s", Score: 5, Evidence: []string{"error: boom", "error: boom again"}}
	if len(Result{Knowledge: []Knowledge{k}}.Clean(nil, text).Knowledge) != 0 {
		t.Error("直った証拠が無いものは捨てる")
	}
	k.Fixed = []string{"ok: deployed"}
	if len(Result{Knowledge: []Knowledge{k}}.Clean(nil, text).Knowledge) != 1 {
		t.Error("証拠がそろえば残す")
	}
}
