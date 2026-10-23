package web

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// OIDC は画面へのログイン。Issuer が空なら使わない（認証なしで動く）。
type OIDC struct {
	Issuer, ClientID, Secret string
	// Redirect は IdP に登録する戻り先（https://aihist.example.com/auth/callback）。
	Redirect string
	// Allow はログインできるメールアドレスか「@ドメイン」。Groups は groups クレームの値。どちらも空なら誰でも。
	Allow, Groups []string
}

const (
	sessionCookie = "aihist_session"
	sessionTTL    = 7 * 24 * time.Hour
)

type login struct {
	cfg      OIDC
	oauth    oauth2.Config
	verifier *oidc.IDTokenVerifier
	key      []byte
	secure   bool
	mu       sync.Mutex
	pending  map[string]pending // state ごと
}

type pending struct {
	nonce, verifier, next string
	at                    time.Time
}

// EnableOIDC は IdP の設定を読み、画面をログインした人だけにする。
func (s *Server) EnableOIDC(ctx context.Context, c OIDC) error {
	if c.ClientID == "" || c.Redirect == "" {
		return errors.New("OIDC: client ID と戻り先の URL が要る")
	}
	p, err := oidc.NewProvider(ctx, c.Issuer)
	if err != nil {
		return err
	}
	key, err := hex.DecodeString(s.st.Setting("session_key", ""))
	if len(key) < 32 || err != nil {
		key = make([]byte, 32)
		rand.Read(key)
		if err := s.st.SetSetting("session_key", hex.EncodeToString(key)); err != nil {
			return err
		}
	}
	s.login = &login{
		cfg: c,
		oauth: oauth2.Config{ClientID: c.ClientID, ClientSecret: c.Secret, RedirectURL: c.Redirect,
			Endpoint: p.Endpoint(), Scopes: []string{oidc.ScopeOpenID, "email", "profile"}},
		verifier: p.Verifier(&oidc.Config{ClientID: c.ClientID}),
		key:      key,
		secure:   strings.HasPrefix(c.Redirect, "https://"),
		pending:  map[string]pending{},
	}
	return nil
}

// open は、ログインしなくても通すもの（API はトークンが守る）。
func open(path string) bool {
	return path == "/healthz" || strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/static/") || strings.HasPrefix(path, "/auth/")
}

func (l *login) guard(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if open(r.URL.Path) || l.user(r) != "" {
			h.ServeHTTP(w, r)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		http.Redirect(w, r, "/auth/login?next="+urlEscape(r.URL.RequestURI()), http.StatusFound)
	})
}

func urlEscape(s string) string {
	return strings.NewReplacer("%", "%25", "&", "%26", "#", "%23", "+", "%2B", "?", "%3F").Replace(s)
}

func randText() string {
	b := make([]byte, 24)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func (l *login) start(w http.ResponseWriter, r *http.Request) {
	next := r.URL.Query().Get("next")
	// 自分のサイトの中だけへ戻す
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/\\") {
		next = "/"
	}
	state, nonce, ver := randText(), randText(), oauth2.GenerateVerifier()
	l.mu.Lock()
	for k, p := range l.pending {
		if time.Since(p.at) > 10*time.Minute {
			delete(l.pending, k)
		}
	}
	l.pending[state] = pending{nonce, ver, next, time.Now()}
	l.mu.Unlock()
	http.Redirect(w, r, l.oauth.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(ver)), http.StatusFound)
}

func (l *login) callback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	l.mu.Lock()
	p, ok := l.pending[q.Get("state")]
	delete(l.pending, q.Get("state"))
	l.mu.Unlock()
	if !ok || time.Since(p.at) > 10*time.Minute {
		http.Redirect(w, r, "/auth/login", http.StatusFound)
		return
	}
	if e := q.Get("error"); e != "" {
		http.Error(w, e, http.StatusForbidden)
		return
	}
	tok, err := l.oauth.Exchange(r.Context(), q.Get("code"), oauth2.VerifierOption(p.verifier))
	if err != nil {
		http.Error(w, "login failed", http.StatusBadGateway)
		return
	}
	raw, _ := tok.Extra("id_token").(string)
	id, err := l.verifier.Verify(r.Context(), raw)
	if err != nil || id.Nonce != p.nonce {
		http.Error(w, "login failed", http.StatusForbidden)
		return
	}
	var c struct {
		Email    string   `json:"email"`
		Verified *bool    `json:"email_verified"`
		Groups   []string `json:"groups"`
	}
	id.Claims(&c)
	if c.Verified != nil && !*c.Verified {
		c.Email = ""
	}
	if !l.allowed(c.Email, c.Groups) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	who := c.Email
	if who == "" {
		who = id.Subject
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: l.sign(who, time.Now().Add(sessionTTL)), Path: "/",
		HttpOnly: true, Secure: l.secure, SameSite: http.SameSiteLaxMode, MaxAge: int(sessionTTL.Seconds())})
	http.Redirect(w, r, p.next, http.StatusFound)
}

func (l *login) allowed(email string, groups []string) bool {
	if len(l.cfg.Allow) == 0 && len(l.cfg.Groups) == 0 {
		return true
	}
	email = strings.ToLower(email)
	for _, a := range l.cfg.Allow {
		a = strings.ToLower(a)
		if email != "" && (email == a || strings.HasPrefix(a, "@") && strings.HasSuffix(email, a)) {
			return true
		}
	}
	for _, g := range groups {
		if slices.Contains(l.cfg.Groups, g) {
			return true
		}
	}
	return false
}

func (l *login) logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Path: "/", MaxAge: -1, HttpOnly: true, Secure: l.secure})
	http.Redirect(w, r, "/", http.StatusFound)
}

// sign はセッションの中身（誰か・いつまで）に署名する。
func (l *login) sign(who string, until time.Time) string {
	v := base64.RawURLEncoding.EncodeToString([]byte(who)) + "." + strconv.FormatInt(until.Unix(), 10)
	m := hmac.New(sha256.New, l.key)
	m.Write([]byte(v))
	return v + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// user はログインしている人。いなければ空。
func (l *login) user(r *http.Request) string {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return ""
	}
	i := strings.LastIndexByte(c.Value, '.')
	if i < 0 {
		return ""
	}
	v, sig := c.Value[:i], c.Value[i+1:]
	m := hmac.New(sha256.New, l.key)
	m.Write([]byte(v))
	got, _ := base64.RawURLEncoding.DecodeString(sig)
	if !hmac.Equal(got, m.Sum(nil)) {
		return ""
	}
	who, exp, _ := strings.Cut(v, ".")
	t, _ := strconv.ParseInt(exp, 10, 64)
	if time.Now().Unix() > t {
		return ""
	}
	b, _ := base64.RawURLEncoding.DecodeString(who)
	return string(b)
}
