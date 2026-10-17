// Package ai は OpenAI と同じ形の API（/v1/chat/completions）を呼び、会話にタグ・要約・ナレッジを付ける。
// 利用者が設定した接続先だけを使い、利用者が押したときだけ動く。
package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Config は接続先。
type Config struct {
	BaseURL string // https://api.openai.com/v1 など
	Key     string
	Model   string
}

func (c Config) Ready() bool { return c.BaseURL != "" && c.Model != "" }

var httpc = &http.Client{Timeout: 5 * time.Minute}

type msg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func (c Config) post(ctx context.Context, path string, body any, out any) error {
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(c.BaseURL, "/")+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Key != "" {
		req.Header.Set("Authorization", "Bearer "+c.Key)
	}
	res, err := httpc.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(res.Body, 32<<20))
	if res.StatusCode/100 != 2 {
		return &APIError{Status: res.StatusCode, Body: strings.TrimSpace(string(rb))}
	}
	return json.Unmarshal(rb, out)
}

// APIError は API が返した失敗。
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string {
	b := e.Body
	if len(b) > 300 {
		b = b[:300]
	}
	return fmt.Sprintf("API %d: %s", e.Status, b)
}

// Chat は system と user を渡し、返事の本文を返す。jsonOut なら JSON で返すよう頼む
// （受け付けない API には頼み方を外してもう一度送る）。
func (c Config) Chat(ctx context.Context, system, user string, jsonOut bool) (string, error) {
	body := map[string]any{
		"model":    c.Model,
		"messages": []msg{{"system", system}, {"user", user}},
	}
	if jsonOut {
		body["response_format"] = map[string]string{"type": "json_object"}
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	err := c.post(ctx, "/chat/completions", body, &out)
	var ae *APIError
	if jsonOut && errors.As(err, &ae) && ae.Status == 400 && strings.Contains(strings.ToLower(ae.Body), "response_format") {
		delete(body, "response_format")
		err = c.post(ctx, "/chat/completions", body, &out)
	}
	if err != nil {
		return "", err
	}
	if len(out.Choices) == 0 {
		return "", errors.New("API が空の返事を返した")
	}
	if out.Choices[0].FinishReason == "length" {
		return "", errors.New("返事が長すぎて途中で切れた")
	}
	if strings.TrimSpace(out.Choices[0].Message.Content) == "" {
		return "", errors.New("AI が空の返事を返した")
	}
	return out.Choices[0].Message.Content, nil
}

// Models は接続先が使えるモデルの名前（設定の画面の候補）。
func (c Config) Models(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(c.BaseURL, "/")+"/models", nil)
	if err != nil {
		return nil, err
	}
	if c.Key != "" {
		req.Header.Set("Authorization", "Bearer "+c.Key)
	}
	res, err := httpc.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return nil, &APIError{Status: res.StatusCode, Body: string(b)}
	}
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return nil, err
	}
	var ids []string
	for _, d := range out.Data {
		ids = append(ids, d.ID)
	}
	sort.Strings(ids)
	return ids, nil
}

// decode は返事から JSON を取り出す（```json で囲まれていても読む）。
func decode(s string, v any) error {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "{"); i > 0 {
		s = s[i:]
	}
	if i := strings.LastIndex(s, "}"); i >= 0 && i < len(s)-1 {
		s = s[:i+1]
	}
	if err := json.Unmarshal([]byte(s), v); err != nil {
		return fmt.Errorf("AI の返事を読めない: %w", err)
	}
	return nil
}
