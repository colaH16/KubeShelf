package shelf

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"path"
	"sort"
	"strings"
	"time"

	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"
)

func favoriteConfigMapName(subject string) string {
	sum := sha256.Sum256([]byte(subject))
	return "kubeshelf-user-" + hex.EncodeToString(sum[:20])
}
func (g *gitRepository) favoritePath(user Identity) string {
	return path.Join(path.Dir(g.cfg.GitPath), "users", favoriteConfigMapName(user.Subject)+".yaml")
}
func encodeFavorites(user Identity, profile FavoriteProfile) ([]byte, error) {
	data, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		return nil, err
	}
	cm := core.ConfigMap{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"}, ObjectMeta: metav1.ObjectMeta{
		Name: favoriteConfigMapName(user.Subject), Namespace: env("POD_NAMESPACE", "public-services"),
		Labels:      map[string]string{"app.kubernetes.io/part-of": "kubeshelf", "kubeshelf.io/user-settings": "true"},
		Annotations: map[string]string{"kubeshelf.io/subject": user.Subject},
	}, Data: map[string]string{"favorites.json": string(data) + "\n"}}
	return yaml.Marshal(cm)
}
func (g *gitRepository) readFavorites(ctx context.Context, user Identity, commit string) (FavoriteProfile, bool, error) {
	file := g.favoritePath(user)
	entry, err := g.run(ctx, nil, "ls-tree", "--name-only", commit, "--", file)
	if err != nil {
		return FavoriteProfile{}, false, err
	}
	if len(bytes.TrimSpace(entry)) == 0 {
		return defaultFavorites(user.Admin), false, nil
	}
	data, err := g.run(ctx, nil, "show", commit+":"+file)
	if err != nil {
		return FavoriteProfile{}, true, err
	}
	var cm core.ConfigMap
	if err := yaml.UnmarshalStrict(data, &cm); err != nil {
		return FavoriteProfile{}, true, errors.New("사용자 ConfigMap 형식이 올바르지 않습니다")
	}
	if cm.Kind != "ConfigMap" || cm.APIVersion != "v1" || cm.Name != favoriteConfigMapName(user.Subject) || cm.Namespace != env("POD_NAMESPACE", "public-services") || cm.Annotations["kubeshelf.io/subject"] != user.Subject || len(cm.Data) != 1 || len(cm.BinaryData) != 0 {
		return FavoriteProfile{}, true, errors.New("사용자 ConfigMap 대상이 일치하지 않습니다")
	}
	var profile FavoriteProfile
	d := json.NewDecoder(strings.NewReader(cm.Data["favorites.json"]))
	d.DisallowUnknownFields()
	if err := d.Decode(&profile); err != nil {
		return profile, true, errors.New("즐겨찾기 파일 형식이 올바르지 않습니다")
	}
	if d.Decode(new(any)) != io.EOF {
		return profile, true, errors.New("즐겨찾기 파일 형식이 올바르지 않습니다")
	}
	if err := validateFavoriteProfile(&profile); err != nil {
		return profile, true, err
	}
	return profile, true, nil
}
func (s *Store) syncFavoriteRepository(ctx context.Context) error {
	if s.cfg.Demo {
		return nil
	}
	settings, commit, err := s.repo.read(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.desired = settings
	s.commit = commit
	s.mu.Unlock()
	return nil
}
func (s *Store) readFavoritesLocked(ctx context.Context, user Identity) (FavoriteProfile, error) {
	if s.cfg.Demo {
		profile, ok := s.profiles[user.Subject]
		if !ok {
			profile = defaultFavorites(user.Admin)
		}
		// Each response owns its slices; callers cannot mutate stored preferences.
		data, _ := json.Marshal(profile)
		var copy FavoriteProfile
		_ = json.Unmarshal(data, &copy)
		return copy, nil
	}
	s.mu.RLock()
	commit := s.commit
	s.mu.RUnlock()
	profile, _, err := s.repo.readFavorites(ctx, user, commit)
	return profile, err
}
func (s *Store) Favorites(ctx context.Context, user Identity) (FavoriteState, error) {
	if user.Subject == "" {
		return FavoriteState{}, errors.New("로그인이 필요합니다")
	}
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	profile, err := s.readFavoritesLocked(ctx, user)
	if err != nil {
		return FavoriteState{}, err
	}
	return favoriteState(profile, user), nil
}

func personalChangeSummary(before, after FavoriteProfile) string {
	changes := []string{}
	oldCollections, _ := json.Marshal(before.Collections)
	newCollections, _ := json.Marshal(after.Collections)
	if !bytes.Equal(oldCollections, newCollections) {
		changes = append(changes, "favorite collections and membership")
	}
	if before.StartCollection != after.StartCollection {
		changes = append(changes, "start view")
	}
	if before.DefaultTarget != after.DefaultTarget {
		changes = append(changes, "default NodePort target")
	}
	oldEndpoints, _ := json.Marshal(before.DefaultEndpoints)
	newEndpoints, _ := json.Marshal(after.DefaultEndpoints)
	if !bytes.Equal(oldEndpoints, newEndpoints) {
		changes = append(changes, "default service addresses")
	}
	return "Update personal settings: " + strings.Join(changes, "; ")
}

func (s *Store) SaveFavorites(ctx context.Context, user Identity, version string, profile FavoriteProfile, accessible Catalog) (FavoriteState, error) {
	if user.Subject == "" {
		return FavoriteState{}, errors.New("로그인이 필요합니다")
	}
	if err := validateFavoriteProfile(&profile); err != nil {
		return FavoriteState{}, err
	}
	if !user.Admin && (len(profile.Collections) != 1 || profile.Collections[0].ID != "favorites" || profile.Collections[0].Name != "즐겨찾기") {
		return FavoriteState{}, errors.New("이 계정은 즐겨찾기 하나를 사용합니다")
	}
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	if err := s.syncFavoriteRepository(ctx); err != nil {
		return FavoriteState{}, err
	}
	before, err := s.readFavoritesLocked(ctx, user)
	if err != nil {
		return FavoriteState{}, err
	}
	current := favoriteState(before, user)
	if current.Version != version {
		return FavoriteState{}, ErrFavoriteConflict
	}
	if profile.DefaultTarget != "" && profile.DefaultTarget != current.Profile.DefaultTarget {
		found := false
		for _, target := range accessible.Targets {
			if target.ID == profile.DefaultTarget {
				found = true
			}
		}
		if !found {
			return FavoriteState{}, errors.New("현재 사용할 수 없는 NodePort 대상입니다")
		}
	}
	for cardID, endpointID := range profile.DefaultEndpoints {
		if current.Profile.DefaultEndpoints[cardID] == endpointID {
			continue
		}
		found := false
		for _, card := range accessible.Cards {
			if card.ID == cardID {
				for _, e := range card.Endpoints {
					if e.ID == endpointID {
						found = true
					}
				}
			}
		}
		if !found {
			return FavoriteState{}, errors.New("현재 볼 수 없는 주소는 기본값으로 지정할 수 없습니다")
		}
	}
	next := favoriteState(profile, user)
	if next.Version == current.Version {
		return current, nil
	}
	if s.cfg.Demo {
		if s.profiles == nil {
			s.profiles = map[string]FavoriteProfile{}
		}
		data, _ := json.Marshal(next.Profile)
		var copy FavoriteProfile
		_ = json.Unmarshal(data, &copy)
		s.profiles[user.Subject] = copy
	} else {
		data, err := encodeFavorites(user, next.Profile)
		if err != nil {
			return FavoriteState{}, err
		}
		// Only this user's server-derived path is staged. No common settings,
		// deployment files or other accounts can be supplied through the API.
		actor := strings.Map(func(r rune) rune {
			if r < 32 || r == 127 {
				return ' '
			}
			return r
		}, user.Username)
		commit, err := s.repo.writeFiles(ctx, map[string][]byte{s.repo.favoritePath(user): data}, personalChangeSummary(current.Profile, next.Profile)+"\n\nEdited by: "+actor)
		if err != nil {
			return FavoriteState{}, err
		}
		s.mu.Lock()
		s.commit = commit
		s.mu.Unlock()
	}
	return next, nil
}

// Provision only absent accounts, in one Git commit. Never reset an existing
// profile or delete settings when an account is disabled or removed.
func (s *Store) EnsureFavoriteAccounts(ctx context.Context, users []Identity) (int, error) {
	if len(users) > 1000 {
		return 0, fmt.Errorf("too many favorite accounts")
	}
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	if err := s.syncFavoriteRepository(ctx); err != nil {
		return 0, err
	}
	if s.cfg.Demo {
		return 0, nil
	}
	s.mu.RLock()
	head := s.commit
	s.mu.RUnlock()
	files := map[string][]byte{}
	settings, _ := s.Desired()
	migrated := false
	initialized := 0
	for _, user := range users {
		if user.Subject == "" || len(user.Subject) > 256 {
			return 0, fmt.Errorf("invalid favorite account")
		}
		profile, exists, err := s.repo.readFavorites(ctx, user, head)
		if err != nil {
			return 0, err
		}
		changed := !exists
		if !exists {
			initialized++
		}
		// Legacy shared address choices belonged to the primary configured admin.
		// Move them once, preserving any newer explicit personal choices.
		if len(s.cfg.AdminSubjects) > 0 && user.Subject == s.cfg.AdminSubjects[0] {
			if settings.DefaultTarget != "" {
				if profile.DefaultTarget == "" {
					profile.DefaultTarget = settings.DefaultTarget
				}
				settings.DefaultTarget = ""
				migrated = true
				changed = true
			}
			for id, a := range settings.Apps {
				if a.DefaultEndpoint == "" {
					continue
				}
				if profile.DefaultEndpoints[id] == "" {
					profile.DefaultEndpoints[id] = a.DefaultEndpoint
				}
				a.DefaultEndpoint = ""
				settings.Apps[id] = a
				migrated = true
				changed = true
			}
		}
		if changed {
			data, err := encodeFavorites(user, profile)
			if err != nil {
				return 0, err
			}
			files[s.repo.favoritePath(user)] = data
		}
	}
	if migrated {
		settings.Revision = randomID()
		data, err := encodeSettings(settings)
		if err != nil {
			return 0, err
		}
		files[s.repo.cfg.GitPath] = data
	}
	if len(files) == 0 {
		return 0, nil
	}
	commit, err := s.repo.writeFiles(ctx, files, fmt.Sprintf("Initialize %d personal settings files; migrate legacy address defaults: %t", initialized, migrated))
	if err != nil {
		return 0, err
	}
	s.mu.Lock()
	s.commit = commit
	if migrated {
		s.desired = settings
	}
	s.mu.Unlock()
	return initialized, nil
}
func (s *Server) RunAccounts(ctx context.Context) {
	if s.cfg.Demo {
		return
	}
	previous := ""
	sync := func() {
		setup, cancel := context.WithTimeout(ctx, 45*time.Second)
		defer cancel()
		directory, err := s.directory.Load(setup)
		if err != nil {
			log.Printf("Favorite account initialization: %v", err)
			return
		}
		users := []Identity{}
		ids := []string{}
		for _, u := range directory.Users {
			users = append(users, Identity{Subject: u.Subject, Username: u.Username, Admin: contains(s.cfg.AdminSubjects, u.Subject)})
			ids = append(ids, u.Subject)
		}
		sort.Strings(ids)
		signature := strings.Join(ids, "\n")
		if signature == previous {
			return
		}
		count, err := s.store.EnsureFavoriteAccounts(setup, users)
		if err != nil {
			log.Printf("Favorite account initialization: %v", err)
			return
		}
		previous = signature
		log.Printf("Favorite accounts ready: %d active, %d initialized", len(users), count)
	}
	sync()
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sync()
		}
	}
}
