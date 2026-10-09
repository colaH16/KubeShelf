package shelf

import "net/url"

// Only application views may be served as SPA pages or used after login.
func isViewPath(path string) bool {
	switch path {
	case "/", "/favorites", "/discovery", "/namespaces", "/hidden":
		return true
	}
	return false
}
func safeReturnTo(path string) string {
	u, err := url.Parse(path)
	if err == nil && u.Scheme == "" && u.Host == "" && u.User == nil && u.Fragment == "" && isViewPath(u.Path) {
		if u.RawQuery == "" {
			return u.Path
		}
		q, err := url.ParseQuery(u.RawQuery)
		if err == nil && u.Path == "/favorites" && len(q) == 1 && len(q["collection"]) == 1 && collectionIDPattern.MatchString(q.Get("collection")) {
			return "/favorites?collection=" + url.QueryEscape(q.Get("collection"))
		}
	}
	return "/"
}
