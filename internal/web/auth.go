package web

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"obblog/internal/account"
	"strings"
	"sync"
	"time"
)

type authentication struct {
	password     [32]byte
	passwordHash string
	secure       bool
	mu           sync.Mutex
	sessions     map[[32]byte]time.Time
	failed       int
	window       time.Time
}

func secureRequest(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https")
}

func (a *App) sessionCookieName() string {
	if a.testMode {
		return "obvault_test_session"
	}
	return "obvault_session"
}

func (a *App) authenticated(r *http.Request) bool {
	cookie, err := r.Cookie(a.sessionCookieName())
	if err != nil {
		return false
	}
	key := sha256.Sum256([]byte(cookie.Value))
	a.auth.mu.Lock()
	defer a.auth.mu.Unlock()
	expires, ok := a.auth.sessions[key]
	if !ok || time.Now().After(expires) {
		delete(a.auth.sessions, key)
		return false
	}
	return true
}

func (a *App) login(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input); err != nil {
		fail(w, 400, "无效的登录请求")
		return
	}
	a.auth.mu.Lock()
	defer a.auth.mu.Unlock()
	now := time.Now()
	if now.Sub(a.auth.window) > time.Minute {
		a.auth.failed = 0
		a.auth.window = now
	}
	if a.auth.failed >= 10 {
		fail(w, 429, "登录尝试过多，请一分钟后重试")
		return
	}
	got := sha256.Sum256([]byte(input.Password))
	valid := subtle.ConstantTimeCompare(got[:], a.auth.password[:]) == 1
	if a.auth.passwordHash != "" {
		valid = account.Verify(a.auth.passwordHash, input.Password)
	}
	if !valid {
		a.auth.failed++
		fail(w, 401, "密码错误")
		return
	}
	a.auth.failed = 0
	for token, expires := range a.auth.sessions {
		if now.After(expires) {
			delete(a.auth.sessions, token)
		}
	}
	// A bounded session set is enough for this single-user backend.
	if len(a.auth.sessions) >= 32 {
		a.auth.sessions = make(map[[32]byte]time.Time)
	}
	token := rand.Text()
	a.auth.sessions[sha256.Sum256([]byte(token))] = now.Add(12 * time.Hour)
	maxAge := 12 * 60 * 60
	if a.testMode {
		maxAge = 0
	}
	http.SetCookie(w, &http.Cookie{Name: a.sessionCookieName(), Value: token, Path: "/admin/", HttpOnly: true, Secure: a.auth.secure || secureRequest(r), SameSite: http.SameSiteStrictMode, MaxAge: maxAge})
	respond(w, map[string]bool{"ok": true})
}

func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(a.sessionCookieName()); err == nil {
		a.auth.mu.Lock()
		delete(a.auth.sessions, sha256.Sum256([]byte(cookie.Value)))
		a.auth.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: a.sessionCookieName(), Path: "/admin/", MaxAge: -1, HttpOnly: true, Secure: a.auth.secure || secureRequest(r), SameSite: http.SameSiteStrictMode})
	respond(w, map[string]bool{"ok": true})
}

func (a *App) gate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.authenticated(r) {
			if len(r.URL.Path) >= 11 && r.URL.Path[:11] == "/admin/api/" {
				fail(w, 401, "登录已过期，请在新标签页重新登录")
			} else {
				http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			}
			return
		}
		// Keep directory reads coherent with a Git checkout or note save.
		if r.Method == "GET" || r.URL.Path == "/admin/api/preview" {
			a.opMu.RLock()
			defer a.opMu.RUnlock()
		} else {
			a.opMu.Lock()
			defer a.opMu.Unlock()
		}
		next.ServeHTTP(w, r)
	})
}
