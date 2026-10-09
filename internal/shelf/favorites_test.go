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

func TestFavoriteAPIUsesSessionOwnerAndNeverExposesOtherAccounts(t *testing.T) {
	cfg := demoConfig()
	store, _ := OpenStore(context.Background(), cfg)
	server := NewServer(cfg, store, NewDemoSource(), "test", fstest.MapFS{"index.html": {Data: []byte("hello")}})
	for _, user := range []Identity{{Subject: "admin", Username: "admin", Admin: true}, {Subject: "family", Username: "family", Groups: []string{"family"}}} {
		server.auth.sessions[user.Subject] = session{Identity: user, CSRF: "csrf-" + user.Subject, Expires: time.Now().Add(time.Hour)}
	}
	request := func(method, owner, body, origin, csrf string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/api/me/favorites", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", origin)
		r.Header.Set("X-CSRF-Token", csrf)
		if owner != "" {
			r.AddCookie(&http.Cookie{Name: server.auth.cookieName(), Value: owner})
		}
		w := httptest.NewRecorder()
		server.Handler().ServeHTTP(w, r)
		return w
	}
	if w := request("GET", "", "", cfg.PublicURL, ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	for _, owner := range []string{"admin", "family"} {
		w := request("GET", owner, "", "", "")
		var state FavoriteState
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &state) != nil {
			t.Fatal(w.Code, w.Body.String())
		}
		state.Profile.Collections[0].Name = map[string]string{"admin": "Admin private collection", "family": "즐겨찾기"}[owner]
		state.Profile.Collections[0].Cards = []string{"opaque-user-reference"}
		b, _ := json.Marshal(state)
		for _, tc := range []struct{ origin, csrf string }{{"https://evil.example", "csrf-" + owner}, {cfg.PublicURL, ""}} {
			if w := request("PUT", owner, string(b), tc.origin, tc.csrf); w.Code != 403 {
				t.Fatal("CSRF bypass", w.Code)
			}
		}
		if w := request("PUT", owner, string(b), cfg.PublicURL, "csrf-"+owner); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	family := request("GET", "family", "", "", "")
	if bytes.Contains(family.Body.Bytes(), []byte("Admin private collection")) {
		t.Fatal("other account leaked")
	}
	if w := request("PUT", "family", `{"subject":"admin","profile":{},"version":"x"}`, cfg.PublicURL, "csrf-family"); w.Code != 400 {
		t.Fatal("client could supply owner")
	}
	// Putting arbitrary opaque references in one's own favorites cannot reveal a service.
	r := httptest.NewRequest("GET", "/api/catalog", nil)
	r.AddCookie(&http.Cookie{Name: server.auth.cookieName(), Value: "family"})
	w := httptest.NewRecorder()
	server.Handler().ServeHTTP(w, r)
	for _, private := range []string{"rancher.example.com", "proxmox.example.com", "Admin private collection", "opaque-user-reference"} {
		if bytes.Contains(w.Body.Bytes(), []byte(private)) {
			t.Fatal("favorite bypassed catalog privacy", private)
		}
	}
}
