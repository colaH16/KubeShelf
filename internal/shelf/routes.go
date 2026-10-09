package shelf

// Only application views may be served as SPA pages or used after login.
func isViewPath(path string) bool {
	switch path {
	case "/", "/favorites", "/discovery", "/namespaces", "/hidden":
		return true
	}
	return false
}
func safeReturnTo(path string) string {
	if isViewPath(path) {
		return path
	}
	return "/"
}
