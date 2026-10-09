package shelf

import (
	"context"
	"net/http/httptest"
	"net/url"
	"testing"
	"testing/fstest"
)

func TestViewRoutesAndAdminAPIAreSeparate(t *testing.T) {
	cfg := demoConfig()
	store, err := OpenStore(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(cfg, store, NewDemoSource(), "test", fstest.MapFS{"index.html": {Data: []byte("<html>app</html>")}, "assets/main.js": {Data: []byte("javascript")}})
	for _, tc := range []struct {
		path   string
		status int
		body   string
	}{
		{"/", 200, "<html>app</html>"}, {"/favorites", 200, "<html>app</html>"}, {"/discovery", 200, "<html>app</html>"}, {"/namespaces", 200, "<html>app</html>"}, {"/hidden", 200, "<html>app</html>"},
		{"/assets/main.js", 200, "javascript"}, {"/assets/missing.js", 404, ""}, {"/unknown", 404, ""}, {"/api/missing", 404, ""}, {"/api/admin/state", 403, ""},
	} {
		w := httptest.NewRecorder()
		server.Handler().ServeHTTP(w, httptest.NewRequest("GET", tc.path, nil))
		if w.Code != tc.status || (tc.body != "" && w.Body.String() != tc.body) {
			t.Errorf("%s: status %d, body %s", tc.path, w.Code, w.Body.String())
		}
	}
	for _, path := range []string{"/namespaces", "/hidden", "/favorites", "/discovery", "https://evil.example/", "//evil.example/", "/api/admin/state", "/favorites?next=evil"} {
		w := httptest.NewRecorder()
		server.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/auth/login?returnTo="+url.QueryEscape(path), nil))
		want := "/"
		if isViewPath(path) {
			want = path
		}
		if w.Code != 303 || w.Header().Get("Location") != want {
			t.Errorf("unsafe or lost return path %q: %d %q", path, w.Code, w.Header().Get("Location"))
		}
	}
}

func TestCollectionReturnDestinations(t *testing.T) {
	for _, path := range []string{"/favorites?collection=daily", "/favorites?collection=ops_1-2"} {
		if got := safeReturnTo(path); got != path {
			t.Errorf("lost collection %q: %q", path, got)
		}
	}
	for _, path := range []string{"//evil.example/favorites?collection=daily", "/favorites?collection=a&collection=b", "/favorites?collection=a&next=evil", "/favorites?collection=../api", "/hidden?collection=a", "/favorites?collection=", "/favorites?collection=a#evil"} {
		if got := safeReturnTo(path); got != "/" {
			t.Errorf("unsafe return %q: %q", path, got)
		}
	}
}
