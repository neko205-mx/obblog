package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testPassword = "temporary-test-password"

func testHandler(t *testing.T, a *App) http.Handler {
	t.Helper()
	h := a.Handler()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/admin/api/login", strings.NewReader(`{"password":"`+testPassword+`"}`)))
	if w.Code != 200 {
		t.Fatalf("login failed: %d %s", w.Code, w.Body.String())
	}
	cookie := w.Result().Cookies()[0]
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { r.AddCookie(cookie); h.ServeHTTP(w, r) })
}

func TestExpiredSessionSecureCookieAndLoginCSRF(t *testing.T) {
	dir := t.TempDir()
	a, err := NewWithOptions(dir, Options{Password: testPassword, SecureCookie: true})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	h := a.Handler()
	r := httptest.NewRequest("POST", "/admin/api/login", strings.NewReader(`{"password":"`+testPassword+`"}`))
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("cross-site login accepted")
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/admin/api/login", strings.NewReader(`{"password":"`+testPassword+`"}`)))
	cookie := w.Result().Cookies()[0]
	if !cookie.Secure {
		t.Fatal("secure cookie flag ignored")
	}
	a.auth.mu.Lock()
	for token := range a.auth.sessions {
		a.auth.sessions[token] = time.Now().Add(-time.Second)
	}
	a.auth.mu.Unlock()
	r = httptest.NewRequest("GET", "/admin/api/vault", nil)
	r.AddCookie(cookie)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("expired session accepted")
	}
}

func TestAdminIsolation(t *testing.T) {
	a, _ := appForTest(t)
	h := a.Handler()
	for _, p := range []string{"/admin/api/vault", "/admin/api/note?path=other.md", "/admin/api/asset?path=photo.svg", "/admin/api/graph", "/admin/api/git"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", p, nil))
		if w.Code != 401 || strings.Contains(w.Body.String(), "# Other") {
			t.Fatalf("private leak %s: %d %s", p, w.Code, w.Body.String())
		}
	}
	for _, p := range []string{"/api/vault", "/api/note?path=other.md", "/api/asset?path=photo.svg", "/api/graph", "/app.js"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", p, nil))
		if w.Code != 404 {
			t.Fatalf("legacy route %s: %d", p, w.Code)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != 200 || strings.Contains(w.Body.String(), "other.md") || !strings.Contains(w.Body.String(), "暂无文章") {
		t.Fatal("public page must be independent")
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/admin/", nil))
	if w.Code != 303 || w.Header().Get("Location") != "/admin/login" {
		t.Fatal("missing login redirect")
	}
}

func TestSessionCookieAutomaticallyUsesHTTPS(t *testing.T) {
	for _, tc := range []struct {
		name, address, forwarded string
		secure                   bool
	}{
		{"local-http", "http://localhost/admin/api/login", "", false},
		{"direct-https", "https://localhost/admin/api/login", "", true},
		{"proxy-https", "http://localhost/admin/api/login", "https", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, _ := appForTest(t)
			h := a.Handler()
			r := httptest.NewRequest("POST", tc.address, strings.NewReader(`{"password":"`+testPassword+`"}`))
			r.Header.Set("X-Forwarded-Proto", tc.forwarded)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 200 || len(w.Result().Cookies()) != 1 || w.Result().Cookies()[0].Secure != tc.secure {
				t.Fatalf("automatic cookie: %d %+v", w.Code, w.Result().Cookies())
			}
			cookie := w.Result().Cookies()[0]
			r = httptest.NewRequest("POST", strings.TrimSuffix(tc.address, "login")+"logout", nil)
			r.Header.Set("X-Forwarded-Proto", tc.forwarded)
			r.AddCookie(cookie)
			w = httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 200 || len(w.Result().Cookies()) != 1 || w.Result().Cookies()[0].Secure != tc.secure {
				t.Fatal("logout cookie transport differs from login")
			}
		})
	}
}

func TestSessionAndCSRF(t *testing.T) {
	a, _ := appForTest(t)
	h := a.Handler()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/admin/api/login", strings.NewReader(`{"password":"wrong"}`)))
	if w.Code != 401 || len(w.Result().Cookies()) != 0 {
		t.Fatal("wrong password accepted")
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/admin/api/login", strings.NewReader(`{"password":"`+testPassword+`"}`)))
	cookie := w.Result().Cookies()[0]
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/admin/" {
		t.Fatal("unsafe cookie")
	}
	r := httptest.NewRequest("POST", "/admin/api/git/pull", nil)
	r.AddCookie(cookie)
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("cross-site write accepted")
	}
	r = httptest.NewRequest("POST", "/admin/api/logout", nil)
	r.AddCookie(cookie)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	r = httptest.NewRequest("GET", "/admin/api/vault", nil)
	r.AddCookie(cookie)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("logout did not revoke session")
	}
	if app, err := NewWithOptions(t.TempDir(), Options{}); err == nil {
		app.Close()
		t.Fatal("empty password accepted")
	}
}
