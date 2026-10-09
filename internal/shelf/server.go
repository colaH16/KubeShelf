package shelf

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type Server struct {
	cfg       Config
	store     *Store
	source    Source
	auth      *Auth
	directory *Directory
	version   string
	assets    fs.FS
}

func NewServer(cfg Config, store *Store, source Source, version string, assets fs.FS) *Server {
	directory := NewDirectory(cfg)
	return &Server{cfg: cfg, store: store, source: source, auth: NewAuth(cfg, directory), directory: directory, version: version, assets: assets}
}
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok\n")) })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if s.store.Applied().Revision == "" {
			http.Error(w, "settings unavailable", 503)
			return
		}
		w.Write([]byte("ready\n"))
	})
	mux.HandleFunc("GET /auth/login", s.auth.Login)
	mux.HandleFunc("GET /auth/callback", s.auth.Callback)
	mux.HandleFunc("POST /auth/logout", s.auth.Logout)
	mux.HandleFunc("GET /api/catalog", func(w http.ResponseWriter, r *http.Request) {
		u, csrf := s.auth.Identity(r)
		cat := BuildCatalog(s.source.Snapshot(), s.store.Applied(), s.cfg, u)
		cat.CSRF = csrf
		cat.Version = s.version
		writeJSON(w, 200, cat)
	})
	mux.HandleFunc("GET /api/me/favorites", s.account(s.getFavorites))
	mux.HandleFunc("PUT /api/me/favorites", s.account(s.putFavorites))
	mux.HandleFunc("GET /api/admin/state", s.admin(func(w http.ResponseWriter, r *http.Request, u Identity) {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		if err := s.store.Refresh(ctx); err != nil {
			writeError(w, 503, err)
			return
		}
		st, commit := s.store.Desired()
		directory, err := s.directory.Load(ctx)
		if err != nil {
			writeError(w, 503, err)
			return
		}
		writeJSON(w, 200, map[string]any{"settings": st, "baseCommit": commit, "status": s.store.Status(), "catalog": BuildCatalog(s.source.Snapshot(), st, s.cfg, u), "directory": directory})
	}))
	mux.HandleFunc("GET /api/admin/status", s.admin(func(w http.ResponseWriter, r *http.Request, u Identity) { writeJSON(w, 200, s.store.Status()) }))
	mux.HandleFunc("PUT /api/admin/state", s.admin(func(w http.ResponseWriter, r *http.Request, u Identity) {
		var req struct {
			BaseCommit      string   `json:"baseCommit"`
			ReviewEndpoints []string `json:"reviewEndpoints"`
			Settings        Settings `json:"settings"`
		}
		if err := decodeBody(w, r, &req); err != nil {
			writeError(w, 400, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
		defer cancel()
		if err := ValidateSettings(&req.Settings); err != nil {
			writeError(w, 400, err)
			return
		}
		current, _ := s.store.Desired()
		if !sameLegacyDefaults(current, req.Settings) {
			writeError(w, 400, errors.New("기본 접속 주소는 개인 설정에서 저장합니다. 페이지를 새로고침해 주세요"))
			return
		}
		if len(req.ReviewEndpoints) > 1000 {
			writeError(w, 400, errors.New("too many review entries"))
			return
		}
		if len(req.ReviewEndpoints) > 0 {
			catalog := BuildCatalog(s.source.Snapshot(), req.Settings, s.cfg, u)
			for _, card := range catalog.Cards {
				for _, e := range card.Endpoints {
					if contains(req.ReviewEndpoints, e.ID) && e.Kind != "custom" {
						a := req.Settings.Apps[e.AppID]
						if a.Reviewed == nil {
							a.Reviewed = map[string]string{}
						}
						a.Reviewed[e.ID] = e.Fingerprint
						req.Settings.Apps[e.AppID] = a
					}
				}
			}
		}
		status, err := s.store.Save(ctx, req.BaseCommit, req.Settings, u.Username)
		if err != nil {
			code := 400
			if errors.Is(err, ErrConflict) {
				code = 409
			}
			writeError(w, code, err)
			return
		}
		saved, commit := s.store.Desired()
		writeJSON(w, 200, map[string]any{"status": status, "baseCommit": commit, "settings": saved})
	}))
	mux.HandleFunc("POST /api/admin/tcp", s.admin(s.checkTCP))
	if s.cfg.Demo {
		mux.HandleFunc("POST /auth/demo", func(w http.ResponseWriter, r *http.Request) {
			if !s.auth.SameOrigin(r) {
				http.Error(w, "invalid origin", 403)
				return
			}
			var v struct {
				Role string `json:"role"`
			}
			if decodeBody(w, r, &v) != nil {
				http.Error(w, "invalid request", 400)
				return
			}
			u := Identity{Subject: "demo-family", Username: "family", Name: "Family", Groups: []string{"family"}}
			if v.Role == "admin" {
				u = Identity{Subject: "demo-admin", Username: "admin", Name: "Administrator", Groups: []string{"operators"}, Admin: true}
			}
			s.auth.newSession(w, r, u, "/")
		})
	}
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if !isViewPath(r.URL.Path) {
			http.FileServer(http.FS(s.assets)).ServeHTTP(w, r)
			return
		}
		b, err := fs.ReadFile(s.assets, "index.html")
		if err != nil {
			http.Error(w, "frontend not built", 503)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(b)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "private, no-store, max-age=0")
		w.Header().Set("CDN-Cache-Control", "no-store")
		w.Header().Set("Cloudflare-CDN-Cache-Control", "no-store")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Vary", "Cookie")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' https: data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		mux.ServeHTTP(w, r)
	})
}
func (s *Server) admin(next func(http.ResponseWriter, *http.Request, Identity)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, csrf := s.auth.Identity(r)
		if !u.Admin {
			writeError(w, 403, errors.New("관리자만 사용할 수 있습니다"))
			return
		}
		if r.Method != "GET" {
			if !s.auth.SameOrigin(r) || csrf == "" || r.Header.Get("X-CSRF-Token") != csrf {
				writeError(w, 403, errors.New("요청 검증에 실패했습니다"))
				return
			}
		}
		next(w, r, u)
	}
}
func decodeBody(w http.ResponseWriter, r *http.Request, v any) error {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		return errors.New("JSON request required")
	}
	r.Body = http.MaxBytesReader(w, r.Body, 800000)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return errors.New("요청 형식이 올바르지 않습니다")
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("요청 형식이 올바르지 않습니다")
	}
	return nil
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func writeError(w http.ResponseWriter, status int, e error) {
	writeJSON(w, status, map[string]string{"error": e.Error()})
}
func (s *Server) checkTCP(w http.ResponseWriter, r *http.Request, u Identity) {
	var req struct {
		EndpointID string `json:"endpointId"`
		TargetID   string `json:"targetId"`
	}
	if err := decodeBody(w, r, &req); err != nil {
		writeError(w, 400, err)
		return
	}
	settings := s.store.Applied()
	var host string
	for _, t := range settings.Targets {
		if t.ID == req.TargetID && t.TCPEnabled {
			host = t.Host
		}
	}
	if host == "" {
		writeError(w, 400, errors.New("TCP 확인이 허용된 수동 도메인을 선택해 주세요"))
		return
	}
	catalog := BuildCatalog(s.source.Snapshot(), settings, s.cfg, u)
	port := int32(0)
	for _, c := range catalog.Cards {
		for _, e := range c.Endpoints {
			if e.ID == req.EndpointID && (e.Protocol == "TCP" || e.Protocol == "") {
				for _, t := range e.Targets {
					if t.TargetID == req.TargetID {
						port = e.NodePort
					}
				}
			}
		}
	}
	if port == 0 {
		writeError(w, 400, errors.New("확인 가능한 NodePort가 없습니다"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(addresses) == 0 {
		writeJSON(w, 200, map[string]any{"reachable": false, "message": "DNS 조회 실패", "checkedAt": time.Now()})
		return
	}
	for _, a := range addresses {
		if a.IP.IsLoopback() || a.IP.IsUnspecified() || a.IP.IsLinkLocalUnicast() || a.IP.IsLinkLocalMulticast() || a.IP.IsMulticast() {
			writeError(w, 400, errors.New("이 주소는 TCP 확인 대상에서 제외됩니다"))
			return
		}
	}
	reachable := false
	for _, a := range addresses {
		conn, e := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp", net.JoinHostPort(a.IP.String(), strconv.Itoa(int(port))))
		if e == nil {
			conn.Close()
			reachable = true
			break
		}
	}
	message := "서버에서 TCP 연결 실패"
	if reachable {
		message = "서버에서 TCP 연결 성공"
	}
	writeJSON(w, 200, map[string]any{"reachable": reachable, "message": message, "checkedAt": time.Now()})
}

func sameLegacyDefaults(a, b Settings) bool {
	if a.DefaultTarget != b.DefaultTarget {
		return false
	}
	for id, x := range a.Apps {
		if x.DefaultEndpoint != b.Apps[id].DefaultEndpoint {
			return false
		}
	}
	for id, x := range b.Apps {
		if x.DefaultEndpoint != a.Apps[id].DefaultEndpoint {
			return false
		}
	}
	return true
}
