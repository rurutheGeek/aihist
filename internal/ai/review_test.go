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
	ks := []Knowledge{{Title: "a"}, {Title: "b"}, {Title: "c"}}
	got := fakeAI(t, `{"keep": [3, 1, 3, 9]}`).review(context.Background(), append([]Knowledge(nil), ks...))
	if len(got) != 2 || got[0].Title != "c" || got[1].Title != "a" {
		t.Errorf("番号の記事だけを1回ずつ残す: %+v", got)
	}
	if got := fakeAI(t, `壊れた返事`).review(context.Background(), ks); len(got) != 3 {
		t.Errorf("見直せなかったときはそのまま: %+v", got)
	}
}
