package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func fakeAI(t *testing.T, reply string) Config {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": reply}}}})
	}))
	t.Cleanup(srv.Close)
	return Config{BaseURL: srv.URL, Model: "m"}
}

func TestReview(t *testing.T) {
	items := make([]string, 23)
	got := fakeAI(t, `{"keep": [3, 1, 3, 99]}`).Review(context.Background(), items)
	// 20 件ずつ見せる。どちらの回も 1 と 3 だけ残す
	for i, k := range got {
		if want := i == 0 || i == 2 || i == 20 || i == 22; k != want {
			t.Errorf("%d: %v", i, k)
		}
	}
	for i, k := range fakeAI(t, `壊れた返事`).Review(context.Background(), items) {
		if !k {
			t.Errorf("見直せなかったときは残す: %d", i)
		}
	}
}
