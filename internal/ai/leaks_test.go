package ai

import (
	"slices"
	"testing"
)

func TestLeaks(t *testing.T) {
	w := Personal("ouchithegeek", "/home/ruru/project/shake-cloud", "feat/cloud-subnets", "cd /home/ruru/x")
	for _, want := range []string{"ouchithegeek", "ruru", "shake", "subnets"} {
		if want == "ruru" {
			continue // 4文字未満は拾わない
		}
		if !slices.Contains(w, want) {
			t.Errorf("Personal %v に %s が無い", w, want)
		}
	}
	got := Leaks("shakecloud-guests を直す。詳しくは https://example.org/x 。サーバー 192.168.10.5", w)
	for _, want := range []string{"shake", "https://example.org/x", "192.168.10.5"} {
		if !slices.Contains(got, want) {
			t.Errorf("Leaks %v に %s が無い", got, want)
		}
	}
	if l := Leaks("Proxmox で iGPU をパススルーする", w); len(l) != 0 {
		t.Errorf("拾いすぎ %v", l)
	}
}
