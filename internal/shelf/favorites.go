package shelf

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type FavoriteCollection struct {
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	Cards []string `json:"cards"`
}
type FavoriteProfile struct {
	DefaultTarget    string               `json:"defaultTarget,omitempty"`
	DefaultEndpoints map[string]string    `json:"defaultEndpoints"`
	Collections      []FavoriteCollection `json:"collections"`
	StartCollection  string               `json:"startCollection"`
}
type FavoriteState struct {
	Profile FavoriteProfile `json:"profile"`
	Version string          `json:"version"`
}

var collectionIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,80}$`)
var ErrFavoriteConflict = errors.New("다른 기기에서 모음이 변경됐어요. 최신 모음을 불러온 뒤 다시 저장해 주세요")

func defaultFavorites(admin bool) FavoriteProfile {
	if !admin {
		return FavoriteProfile{Collections: []FavoriteCollection{{"favorites", "즐겨찾기", []string{}}}, DefaultEndpoints: map[string]string{}}
	}
	return FavoriteProfile{Collections: []FavoriteCollection{{"daily", "일상", []string{}}, {"management", "관리", []string{}}, {"monitoring", "모니터링", []string{}}}, DefaultEndpoints: map[string]string{}}
}
func favoriteState(profile FavoriteProfile, user Identity) FavoriteState {
	if !user.Admin {
		cards := []string{}
		for _, c := range profile.Collections {
			cards = append(cards, c.Cards...)
		}
		profile.Collections = []FavoriteCollection{{"favorites", "즐겨찾기", sortedUnique(cards)}}
		if profile.StartCollection != "" {
			profile.StartCollection = "favorites"
		}
	}
	data, _ := json.Marshal(profile)
	sum := sha256.Sum256(data)
	return FavoriteState{Profile: profile, Version: hex.EncodeToString(sum[:])}
}
func validateFavoriteProfile(p *FavoriteProfile) error {
	if p.DefaultEndpoints == nil {
		p.DefaultEndpoints = map[string]string{}
	}
	if len(p.DefaultTarget) > 300 || len(p.DefaultEndpoints) > 2000 {
		return errors.New("기본 주소 설정이 너무 많습니다")
	}
	for card, endpoint := range p.DefaultEndpoints {
		if card == "" || endpoint == "" || len(card) > 100 || len(endpoint) > 100 {
			return errors.New("기본 주소 설정이 올바르지 않습니다")
		}
	}
	if p.Collections == nil {
		p.Collections = []FavoriteCollection{}
	}
	if len(p.Collections) > 20 {
		return errors.New("모음은 최대 20개까지 만들 수 있습니다")
	}
	ids, names := map[string]bool{}, map[string]bool{}
	references := 0
	for i := range p.Collections {
		c := &p.Collections[i]
		c.Name = strings.TrimSpace(c.Name)
		if !collectionIDPattern.MatchString(c.ID) || ids[c.ID] || c.Name == "" || utf8.RuneCountInString(c.Name) > 40 || strings.IndexFunc(c.Name, unicode.IsControl) >= 0 || names[strings.ToLower(c.Name)] {
			return errors.New("모음 이름은 중복 없이 1~40자로 입력해 주세요")
		}
		ids[c.ID], names[strings.ToLower(c.Name)] = true, true
		if c.Cards == nil {
			c.Cards = []string{}
		}
		seen := map[string]bool{}
		for _, id := range c.Cards {
			if id == "" || len(id) > 100 || seen[id] {
				return errors.New("즐겨찾기 서비스 목록이 올바르지 않습니다")
			}
			seen[id] = true
		}
		references += len(c.Cards)
	}
	if references > 2000 {
		return errors.New("즐겨찾기 항목이 너무 많습니다")
	}
	if p.StartCollection != "" && !ids[p.StartCollection] {
		return errors.New("시작 모음을 선택해 주세요")
	}
	return nil
}
func (s *Server) account(next func(http.ResponseWriter, *http.Request, Identity)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, csrf := s.auth.Identity(r)
		if user.Subject == "" {
			writeError(w, 401, errors.New("로그인이 필요합니다"))
			return
		}
		if r.Method != "GET" && (!s.auth.SameOrigin(r) || csrf == "" || r.Header.Get("X-CSRF-Token") != csrf) {
			writeError(w, 403, errors.New("요청 검증에 실패했습니다"))
			return
		}
		next(w, r, user)
	}
}
func (s *Server) getFavorites(w http.ResponseWriter, r *http.Request, user Identity) {
	// Successful pushes are durable immediately. Permissions still use Applied().
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	state, err := s.store.Favorites(ctx, user)
	if err != nil {
		writeError(w, 503, err)
		return
	}
	writeJSON(w, 200, state)
}
func (s *Server) putFavorites(w http.ResponseWriter, r *http.Request, user Identity) {
	var request FavoriteState
	if err := decodeBody(w, r, &request); err != nil {
		writeError(w, 400, err)
		return
	}
	accessible := BuildCatalog(s.source.Snapshot(), s.store.Applied(), s.cfg, user)
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	result, err := s.store.SaveFavorites(ctx, user, request.Version, request.Profile, accessible)
	if err != nil {
		code := 400
		if errors.Is(err, ErrFavoriteConflict) {
			code = 409
		}
		writeError(w, code, err)
		return
	}
	writeJSON(w, 200, result)
}
