package shelf

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func TestAdminWritesRequireSessionOriginAndCSRF(t *testing.T) {
	cfg := demoConfig()
	store, err := OpenStore(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	s := NewServer(cfg, store, NewDemoSource(), "test", fstest.MapFS{"index.html": {Data: []byte("hello")}})
	s.auth.sessions["test-session"] = session{Identity: Identity{Subject: "admin", Admin: true}, CSRF: "expected-token", Expires: time.Now().Add(time.Hour)}
	cases := []struct {
		name, origin, csrf, cookie string
		want                       int
	}{{"anonymous", cfg.PublicURL, "expected-token", "", 403}, {"foreign origin", "https://evil.example", "expected-token", "test-session", 403}, {"missing origin", "", "expected-token", "test-session", 403}, {"missing CSRF", cfg.PublicURL, "", "test-session", 403}, {"authorized invalid payload", cfg.PublicURL, "expected-token", "test-session", 400}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPut, "/api/admin/state", strings.NewReader(`{}`))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("X-CSRF-Token", tc.csrf)
			if tc.cookie != "" {
				r.AddCookie(&http.Cookie{Name: s.auth.cookieName(), Value: tc.cookie})
			}
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
				t.Fatal("personalized response permits caching")
			}
		})
	}
}
func TestAnonymousCatalogOmitsInternalServices(t *testing.T) {
	cfg := demoConfig()
	store, _ := OpenStore(context.Background(), cfg)
	s := NewServer(cfg, store, NewDemoSource(), "test", fstest.MapFS{"index.html": {Data: []byte("hello")}})
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/catalog", nil))
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	for _, value := range []string{"rancher.example.com", "proxmox.example.com", "local-console", "new-project", "directory.example.com"} {
		if strings.Contains(w.Body.String(), value) {
			t.Errorf("private field leaked: %s", value)
		}
	}
}

func TestReviewUsesSavedAddressFingerprint(t *testing.T) {
	cfg := demoConfig()
	store, _ := OpenStore(context.Background(), cfg)
	source := NewDemoSource()
	s := NewServer(cfg, store, source, "test", fstest.MapFS{"index.html": {Data: []byte("hello")}})
	s.auth.sessions["review-session"] = session{Identity: Identity{Subject: "admin", Admin: true}, CSRF: "csrf", Expires: time.Now().Add(time.Hour)}
	st, base := store.Desired()
	card := findCard(t, BuildCatalog(source.Snapshot(), st, cfg, Identity{Admin: true}), "nextcloud")
	e := card.Endpoints[0]
	a := st.Apps[e.AppID]
	if a.Endpoints == nil {
		a.Endpoints = map[string]EndpointSettings{}
	}
	a.Endpoints[e.ID] = EndpointSettings{URL: "https://changed.example.com/"}
	st.Apps[e.AppID] = a
	body, _ := json.Marshal(map[string]any{"settings": st, "baseCommit": base, "reviewEndpoints": []string{e.ID}})
	r := httptest.NewRequest("PUT", "/api/admin/state", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", cfg.PublicURL)
	r.Header.Set("X-CSRF-Token", "csrf")
	r.AddCookie(&http.Cookie{Name: s.auth.cookieName(), Value: "review-session"})
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var result struct {
		Settings Settings `json:"settings"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	saved := findCard(t, BuildCatalog(source.Snapshot(), result.Settings, cfg, Identity{Admin: true}), "nextcloud")
	for _, v := range saved.Endpoints {
		if v.ID == e.ID && result.Settings.Apps[e.AppID].Reviewed[e.ID] != v.Fingerprint {
			t.Fatal("review fingerprint did not match the saved URL")
		}
	}
}
