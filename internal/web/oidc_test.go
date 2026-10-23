package web

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/rurutheGeek/aihist/internal/raw"
	"github.com/rurutheGeek/aihist/internal/store"
)

// fakeIdP は、code を 1 つだけ受け付け、email を入れた ID トークンを返す。
func fakeIdP(t *testing.T, email string, groups []string) *httptest.Server {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	sig, _ := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: jose.JSONWebKey{Key: key, KeyID: "k"}}, nil)
	var srv *httptest.Server
	var nonce string
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"issuer": srv.URL, "authorization_endpoint": srv.URL + "/auth",
			"token_endpoint": srv.URL + "/token", "jwks_uri": srv.URL + "/jwks", "id_token_signing_alg_values_supported": []string{"RS256"}})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "k", Algorithm: "RS256", Use: "sig"}}})
	})
	mux.HandleFunc("/auth", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("code_challenge") == "" {
			t.Error("PKCE が無い")
		}
		nonce = q.Get("nonce")
		http.Redirect(w, r, q.Get("redirect_uri")+"?code=c&state="+url.QueryEscape(q.Get("state")), http.StatusFound)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.Form.Get("code") != "c" || r.Form.Get("code_verifier") == "" {
			http.Error(w, "bad", 400)
			return
		}
		raw, _ := jwt.Signed(sig).Claims(map[string]any{"iss": srv.URL, "aud": "cid", "sub": "u1", "email": email,
			"email_verified": true, "groups": groups, "nonce": nonce, "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix()}).Serialize()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"access_token": "a", "token_type": "Bearer", "id_token": raw})
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func loginServer(t *testing.T, email string, allow []string) (*httptest.Server, *http.Client) {
	idp := fakeIdP(t, email, []string{"staff"})
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s := New(st, raw.Store{Dir: t.TempDir()}, "tok")
	var app *httptest.Server
	app = httptest.NewUnstartedServer(nil)
	app.Start()
	t.Cleanup(app.Close)
	if err := s.EnableOIDC(context.Background(), OIDC{Issuer: idp.URL, ClientID: "cid", Secret: "x", Redirect: app.URL + "/auth/callback", Allow: allow}); err != nil {
		t.Fatal(err)
	}
	app.Config.Handler = s.Handler()
	jar, _ := cookiejar.New(nil)
	return app, &http.Client{Jar: jar}
}

func TestOIDCLogin(t *testing.T) {
	app, c := loginServer(t, "me@example.com", []string{"@example.com"})
	// ログインしていなければ IdP へ回され、戻ると元の画面へ着く
	res, err := c.Get(app.URL + "/settings")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 || res.Request.URL.Path != "/settings" {
		t.Fatalf("got %d %s", res.StatusCode, res.Request.URL)
	}
	// API はトークンで守られ、ログインの対象外
	res, _ = http.Get(app.URL + "/api/files")
	if res.StatusCode != 401 {
		t.Errorf("api: %d", res.StatusCode)
	}
	// クッキーの無い POST は通さない
	res, _ = http.Post(app.URL+"/settings/updates", "application/x-www-form-urlencoded", strings.NewReader(""))
	if res.StatusCode != 401 {
		t.Errorf("post: %d", res.StatusCode)
	}
}

func TestOIDCDenied(t *testing.T) {
	app, c := loginServer(t, "who@other.org", []string{"@example.com"})
	res, err := c.Get(app.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatalf("got %d", res.StatusCode)
	}
}

func TestSession(t *testing.T) {
	l := &login{key: []byte(strings.Repeat("k", 32))}
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: l.sign("a@b", time.Now().Add(time.Hour))})
	if l.user(r) != "a@b" {
		t.Error("正しいセッションが通らない")
	}
	for _, v := range []string{l.sign("a@b", time.Now().Add(-time.Second)), l.sign("a@b", time.Now().Add(time.Hour)) + "x", "YUBi.9999999999.AAAA"} {
		r := httptest.NewRequest("GET", "/", nil)
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: v})
		if l.user(r) != "" {
			t.Errorf("通ってしまう: %s", v)
		}
	}
}

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{{"v0.35.0", "v0.34.2", true}, {"v0.34.2", "v0.34.2", false}, {"v0.9.0", "v0.10.0", false}, {"v1.0.0", "dev", false}} {
		if Newer(c.a, c.b) != c.want {
			t.Errorf("Newer(%s,%s)", c.a, c.b)
		}
	}
}
