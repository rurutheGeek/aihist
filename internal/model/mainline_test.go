package model

import "testing"

func TestMainLine(t *testing.T) {
	ms := []Message{
		{ExternalID: "a", Role: "user"},
		{ExternalID: "b", ParentID: "a", Role: "assistant"},
		{ExternalID: "c", ParentID: "b", Role: "user"},      // 巻き戻して捨てた発言
		{ExternalID: "d", ParentID: "c", Role: "assistant"}, // その答え
		{ExternalID: "e", ParentID: "b", Role: "user"},      // 送り直した発言
		{ExternalID: "f", ParentID: "e", Role: "assistant"},
		{ExternalID: "g", ParentID: "f", Role: "assistant"}, // 途中で止めた答え（残す）
		{ExternalID: "h", ParentID: "f", Role: "user"},
	}
	var got string
	for _, m := range MainLine(ms) {
		got += m.ExternalID
	}
	if got != "abefgh" {
		t.Fatal(got)
	}
}
