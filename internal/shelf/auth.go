package shelf

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

type DirectoryUser struct {
	Subject  string   `json:"subject"`
	Username string   `json:"username"`
	Name     string   `json:"name"`
	Groups   []string `json:"groups"`
}
type DirectoryData struct {
	Users  []DirectoryUser `json:"users"`
	Groups []string        `json:"groups"`
}
type Directory struct {
	cfg     Config
	mu      sync.Mutex
	data    DirectoryData
	expires time.Time
	client  *http.Client
}

func NewDirectory(c Config) *Directory {
	return &Directory{cfg: c, client: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (d *Directory) Load(ctx context.Context) (DirectoryData, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.cfg.Demo {
		return DirectoryData{Users: []DirectoryUser{{Subject: "demo-admin", Username: "admin", Name: "Administrator", Groups: []string{"operators"}}, {Subject: "demo-family", Username: "family", Name: "Family", Groups: []string{"family"}}}, Groups: []string{"operators", "family"}}, nil
	}
	if time.Now().Before(d.expires) {
		return d.data, nil
	}
	if d.cfg.DirectoryURL == "" {
		return DirectoryData{}, fmt.Errorf("사용자 디렉터리가 구성되지 않았습니다")
	}
	secret, err := os.ReadFile(d.cfg.DirectoryTokenFile)
	if err != nil {
		return DirectoryData{}, fmt.Errorf("사용자 디렉터리 인증 정보를 읽을 수 없습니다")
	}
	data := DirectoryData{Users: []DirectoryUser{}, Groups: []string{}}
	for page := 1; page <= 100; page++ {
		endpoint := strings.TrimRight(d.cfg.DirectoryURL, "/") + "/api/v3/core/users/?page_size=100&page=" + strconv.Itoa(page)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return data, err
		}
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(secret)))
		res, err := d.client.Do(req)
		if err != nil {
			return data, fmt.Errorf("사용자 디렉터리에 연결할 수 없습니다")
		}
		body, e := io.ReadAll(io.LimitReader(res.Body, 4<<20))
		res.Body.Close()
		if e != nil || res.StatusCode != 200 {
			return data, fmt.Errorf("사용자 디렉터리 조회에 실패했습니다")
		}
		var list struct {
			Pagination struct {
				Next int `json:"next"`
			} `json:"pagination"`
			Results []struct {
				UUID     string `json:"uuid"`
				Username string `json:"username"`
				Name     string `json:"name"`
				Active   bool   `json:"is_active"`
				Type     string `json:"type"`
				Groups   []struct {
					Name string `json:"name"`
				} `json:"groups_obj"`
			} `json:"results"`
		}
		if err := json.Unmarshal(body, &list); err != nil {
			return data, fmt.Errorf("사용자 디렉터리 응답 오류")
		}
		for _, u := range list.Results {
			if !u.Active || u.UUID == "" || strings.Contains(u.Type, "service_account") {
				continue
			}
			du := DirectoryUser{Subject: u.UUID, Username: u.Username, Name: u.Name, Groups: []string{}}
			for _, g := range u.Groups {
				du.Groups = append(du.Groups, g.Name)
				data.Groups = append(data.Groups, g.Name)
			}
			data.Users = append(data.Users, du)
		}
		if list.Pagination.Next == 0 {
			break
		}
		if page == 100 {
			return data, fmt.Errorf("사용자 디렉터리가 너무 큽니다")
		}
	}
	// Include empty groups as well, so permissions can be assigned before membership.
	for page := 1; page <= 100; page++ {
		endpoint := strings.TrimRight(d.cfg.DirectoryURL, "/") + "/api/v3/core/groups/?page_size=100&page=" + strconv.Itoa(page)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(secret)))
		res, err := d.client.Do(req)
		if err != nil {
			return data, fmt.Errorf("그룹 디렉터리 조회 실패")
		}
		body, e := io.ReadAll(io.LimitReader(res.Body, 4<<20))
		res.Body.Close()
		if e != nil || res.StatusCode != 200 {
			return data, fmt.Errorf("그룹 디렉터리 조회 실패")
		}
		var list struct {
			Pagination struct {
				Next int `json:"next"`
			} `json:"pagination"`
			Results []struct {
				Name string `json:"name"`
			} `json:"results"`
		}
		if err = json.Unmarshal(body, &list); err != nil {
			return data, err
		}
		for _, g := range list.Results {
			data.Groups = append(data.Groups, g.Name)
		}
		if list.Pagination.Next == 0 {
			break
		}
		if page == 100 {
			return data, fmt.Errorf("그룹 디렉터리가 너무 큽니다")
		}
	}
	data.Groups = sortedUnique(data.Groups)
	sort.Slice(data.Users, func(i, j int) bool { return data.Users[i].Username < data.Users[j].Username })
	d.data = data
	d.expires = time.Now().Add(30 * time.Second)
	return data, nil
}

type session struct {
	Identity Identity
	CSRF     string
	Expires  time.Time
}
type loginAttempt struct {
	Nonce, Verifier string
	Expires         time.Time
}
type Auth struct {
	cfg        Config
	directory  *Directory
	mu         sync.Mutex
	sessions   map[string]session
	attempts   map[string]loginAttempt
	providerMu sync.Mutex
	provider   *oidc.Provider
	client     *http.Client
}

func NewAuth(c Config, d *Directory) *Auth {
	return &Auth{cfg: c, directory: d, sessions: map[string]session{}, attempts: map[string]loginAttempt{}, client: &http.Client{Timeout: 12 * time.Second}}
}
func (a *Auth) cookieName() string {
	if a.cfg.Demo {
		return "kubeshelf-demo"
	}
	return "__Host-kubeshelf"
}
func (a *Auth) setCookie(w http.ResponseWriter, name, value string, age int) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", MaxAge: age, Secure: !a.cfg.Demo, HttpOnly: true, SameSite: http.SameSiteLaxMode})
}
func (a *Auth) oauth(ctx context.Context) (*oidc.Provider, *oauth2.Config, error) {
	ctx = oidc.ClientContext(ctx, a.client)
	a.providerMu.Lock()
	defer a.providerMu.Unlock()
	if a.provider == nil {
		p, err := oidc.NewProvider(ctx, a.cfg.OIDCIssuer)
		if err != nil {
			return nil, nil, errors.New("SSO 서버에 연결할 수 없습니다")
		}
		a.provider = p
	}
	secret, err := os.ReadFile(a.cfg.OIDCSecretFile)
	if err != nil {
		return nil, nil, errors.New("SSO 인증 정보를 읽을 수 없습니다")
	}
	return a.provider, &oauth2.Config{ClientID: a.cfg.OIDCClientID, ClientSecret: strings.TrimSpace(string(secret)), Endpoint: a.provider.Endpoint(), RedirectURL: a.cfg.PublicURL + "/auth/callback", Scopes: []string{oidc.ScopeOpenID, "profile", "email"}}, nil
}
func (a *Auth) Identity(r *http.Request) (Identity, string) {
	cookie, err := r.Cookie(a.cookieName())
	if err != nil {
		return Identity{Groups: []string{}}, ""
	}
	a.mu.Lock()
	s, ok := a.sessions[cookie.Value]
	if ok && time.Now().After(s.Expires) {
		delete(a.sessions, cookie.Value)
		ok = false
	}
	a.mu.Unlock()
	if !ok {
		return Identity{Groups: []string{}}, ""
	}
	if a.cfg.Demo {
		return s.Identity, s.CSRF
	}
	data, err := a.directory.Load(r.Context())
	if err != nil {
		return Identity{Groups: []string{}}, ""
	}
	for _, u := range data.Users {
		if u.Subject == s.Identity.Subject {
			return Identity{Subject: u.Subject, Username: u.Username, Name: u.Name, Groups: u.Groups, Admin: contains(a.cfg.AdminSubjects, u.Subject)}, s.CSRF
		}
	}
	a.mu.Lock()
	delete(a.sessions, cookie.Value)
	a.mu.Unlock()
	return Identity{Groups: []string{}}, ""
}
func (a *Auth) newSession(w http.ResponseWriter, r *http.Request, u Identity) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if old, err := r.Cookie(a.cookieName()); err == nil {
		delete(a.sessions, old.Value)
	}
	for id, s := range a.sessions {
		if time.Now().After(s.Expires) {
			delete(a.sessions, id)
		}
	}
	if len(a.sessions) >= 10000 {
		http.Error(w, "로그인 세션이 너무 많습니다", http.StatusServiceUnavailable)
		return
	}
	id := randomID()
	a.sessions[id] = session{Identity: u, CSRF: randomID(), Expires: time.Now().Add(time.Hour)}
	a.setCookie(w, a.cookieName(), id, 3600)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
func (a *Auth) Login(w http.ResponseWriter, r *http.Request) {
	if a.cfg.Demo {
		a.newSession(w, r, Identity{Subject: "demo-admin", Name: "Administrator", Username: "admin", Groups: []string{"operators"}, Admin: true})
		return
	}
	_, cfg, err := a.oauth(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	state, nonce, verifier := randomID(), randomID(), oauth2.GenerateVerifier()
	a.mu.Lock()
	for id, v := range a.attempts {
		if time.Now().After(v.Expires) {
			delete(a.attempts, id)
		}
	}
	if len(a.attempts) >= 2000 {
		a.mu.Unlock()
		http.Error(w, "잠시 후 다시 시도해 주세요", http.StatusTooManyRequests)
		return
	}
	a.attempts[state] = loginAttempt{Nonce: nonce, Verifier: verifier, Expires: time.Now().Add(5 * time.Minute)}
	a.mu.Unlock()
	a.setCookie(w, a.cookieName()+"-login", state, 300)
	http.Redirect(w, r, cfg.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier)), http.StatusFound)
}
func (a *Auth) Callback(w http.ResponseWriter, r *http.Request) {
	if a.cfg.Demo {
		http.NotFound(w, r)
		return
	}
	state := r.URL.Query().Get("state")
	cookie, err := r.Cookie(a.cookieName() + "-login")
	if err != nil || state == "" || cookie.Value != state {
		http.Error(w, "로그인 요청이 일치하지 않습니다", 400)
		return
	}
	a.mu.Lock()
	pending, ok := a.attempts[state]
	delete(a.attempts, state)
	a.mu.Unlock()
	a.setCookie(w, a.cookieName()+"-login", "", -1)
	if !ok || time.Now().After(pending.Expires) {
		http.Error(w, "로그인 요청이 만료됐습니다", 400)
		return
	}
	provider, cfg, err := a.oauth(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 503)
		return
	}
	ctx := oidc.ClientContext(r.Context(), a.client)
	token, err := cfg.Exchange(ctx, r.URL.Query().Get("code"), oauth2.VerifierOption(pending.Verifier))
	if err != nil {
		http.Error(w, "SSO 코드 확인에 실패했습니다", 401)
		return
	}
	raw, ok := token.Extra("id_token").(string)
	if !ok {
		http.Error(w, "SSO 토큰이 없습니다", 401)
		return
	}
	verified, err := provider.Verifier(&oidc.Config{ClientID: a.cfg.OIDCClientID}).Verify(ctx, raw)
	if err != nil || verified.Nonce != pending.Nonce {
		http.Error(w, "SSO 토큰 검증에 실패했습니다", 401)
		return
	}
	users, err := a.directory.Load(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 503)
		return
	}
	for _, u := range users.Users {
		if u.Subject == verified.Subject {
			a.newSession(w, r, Identity{Subject: u.Subject, Username: u.Username, Name: u.Name, Groups: u.Groups, Admin: contains(a.cfg.AdminSubjects, u.Subject)})
			return
		}
	}
	http.Error(w, "활성화된 사용자만 로그인할 수 있습니다", 403)
}
func (a *Auth) SameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	u, e := url.Parse(origin)
	return e == nil && u.Scheme+"://"+u.Host == a.cfg.PublicURL && u.Path == ""
}
func (a *Auth) Logout(w http.ResponseWriter, r *http.Request) {
	_, csrf := a.Identity(r)
	if !a.SameOrigin(r) || csrf != "" && r.Header.Get("X-CSRF-Token") != csrf {
		http.Error(w, "요청 검증에 실패했습니다", 403)
		return
	}
	if cookie, err := r.Cookie(a.cookieName()); err == nil {
		a.mu.Lock()
		delete(a.sessions, cookie.Value)
		a.mu.Unlock()
	}
	a.setCookie(w, a.cookieName(), "", -1)
	w.WriteHeader(http.StatusNoContent)
}
