package shelf

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestAccountConfigMapsInitializeOnceAndMigrateDefaults(t *testing.T) {
	store, remote, _ := newTestStore(t)
	admin := Identity{Subject: "admin-uuid", Username: "admin", Admin: true}
	family := Identity{Subject: "family-uuid", Username: "family"}
	store.cfg.AdminSubjects = []string{admin.Subject}
	settings, base := store.Desired()
	settings.Namespaces["infra"] = Policy{Mode: "admin", Hidden: true}
	settings.DefaultTarget = "node-node3"
	settings.Apps["cloud"] = AppSettings{Name: "Cloud", DefaultEndpoint: "cloud-private"}
	if _, err := store.Save(context.Background(), base, settings, "admin"); err != nil {
		t.Fatal(err)
	}
	before := gitTest(t, remote, "rev-list", "--count", "HEAD")
	n, err := store.EnsureFavoriteAccounts(context.Background(), []Identity{admin, family})
	if err != nil || n != 2 {
		t.Fatal(n, err)
	}
	if before != "2" || gitTest(t, remote, "rev-list", "--count", "HEAD") != "3" {
		t.Fatal("initialization must batch its writes into one commit")
	}
	a, err := store.Favorites(context.Background(), admin)
	if err != nil {
		t.Fatal(err)
	}
	b, err := store.Favorites(context.Background(), family)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Profile.Collections) != 3 || len(b.Profile.Collections) != 1 {
		t.Fatal("wrong account defaults")
	}
	if a.Profile.DefaultTarget != "node-node3" || a.Profile.DefaultEndpoints["cloud"] != "cloud-private" || b.Profile.DefaultTarget != "" || len(b.Profile.DefaultEndpoints) != 0 {
		t.Fatal("legacy defaults were not isolated to the primary admin")
	}
	after, _ := store.Desired()
	if after.DefaultTarget != "" || after.Apps["cloud"].DefaultEndpoint != "" || after.Apps["cloud"].Name != "Cloud" || !after.Namespaces["infra"].Hidden {
		t.Fatal("migration changed unrelated shared settings")
	}
	changed := strings.Fields(gitTest(t, remote, "diff-tree", "--no-commit-id", "--name-only", "-r", "HEAD"))
	if len(changed) != 3 {
		t.Fatal("expected two user ConfigMaps plus shared default cleanup", changed)
	}
	for _, user := range []Identity{admin, family} {
		blob := gitTest(t, remote, "show", "HEAD:"+store.repo.favoritePath(user))
		if !strings.Contains(blob, "kind: ConfigMap") || !strings.Contains(blob, "name: "+favoriteConfigMapName(user.Subject)) || !strings.Contains(blob, "favorites.json:") {
			t.Fatal("invalid user ConfigMap")
		}
	}
	head := gitTest(t, remote, "rev-parse", "HEAD")
	if n, err := store.EnsureFavoriteAccounts(context.Background(), []Identity{admin, family}); n != 0 || err != nil {
		t.Fatal(n, err)
	}
	if gitTest(t, remote, "rev-parse", "HEAD") != head {
		t.Fatal("restart rewrote existing profiles")
	}
}

func TestDifferentAccountSavesAndSharedSettingsDoNotConflict(t *testing.T) {
	store, remote, _ := newTestStore(t)
	a := Identity{Subject: "a", Username: "a", Admin: true}
	b := Identity{Subject: "b", Username: "b"}
	_, _ = store.EnsureFavoriteAccounts(context.Background(), []Identity{a, b})
	shared, base := store.Desired()
	sa, _ := store.Favorites(context.Background(), a)
	sb, _ := store.Favorites(context.Background(), b)
	sa.Profile.Collections[0].Cards = []string{"cloud"}
	sb.Profile.Collections[0].Cards = []string{"photos"}
	var wg sync.WaitGroup
	failures := make(chan error, 2)
	for _, entry := range []struct {
		user  Identity
		state FavoriteState
	}{{a, sa}, {b, sb}} {
		wg.Add(1)
		go func(user Identity, state FavoriteState) {
			defer wg.Done()
			_, err := store.SaveFavorites(context.Background(), user, state.Version, state.Profile, Catalog{})
			failures <- err
		}(entry.user, entry.state)
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	// An editor opened before either account save can still edit the shared file.
	shared.Namespaces["new"] = Policy{Mode: "admin"}
	if _, err := store.Save(context.Background(), base, shared, "admin"); err != nil {
		t.Fatal("unrelated personal commit conflicted with shared settings", err)
	}
	if _, err := store.SaveFavorites(context.Background(), a, sa.Version, sa.Profile, Catalog{}); !errors.Is(err, ErrFavoriteConflict) {
		t.Fatal("same-account stale save not rejected", err)
	}
	// Fresh in-memory state recovers both profiles from Git without a volume.
	fresh := &Store{cfg: store.cfg, repo: store.repo}
	if err := fresh.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []struct {
		user Identity
		card string
	}{{a, "cloud"}, {b, "photos"}} {
		state, err := fresh.Favorites(context.Background(), entry.user)
		if err != nil || len(state.Profile.Collections[0].Cards) != 1 || state.Profile.Collections[0].Cards[0] != entry.card {
			t.Fatal("persisted account lost", state, err)
		}
	}
	if strings.TrimSpace(gitTest(t, remote, "diff-tree", "--no-commit-id", "--name-only", "-r", "HEAD")) != "runtime/settings.yaml" {
		t.Fatal("shared save rewrote a personal file")
	}
}

func TestFavoriteRejectedPushNeverBecomesVisible(t *testing.T) {
	store, remote, _ := newTestStore(t)
	user := Identity{Subject: "owner", Admin: true}
	_, _ = store.EnsureFavoriteAccounts(context.Background(), []Identity{user})
	before, _ := store.Favorites(context.Background(), user)
	profile := before.Profile
	profile.Collections[0].Cards = []string{"kept-only-in-browser"}
	if err := os.WriteFile(filepath.Join(remote, "hooks/pre-receive"), []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveFavorites(context.Background(), user, before.Version, profile, Catalog{}); err == nil {
		t.Fatal("rejected push reported success")
	}
	after, err := store.Favorites(context.Background(), user)
	if err != nil || after.Version != before.Version || len(after.Profile.Collections[0].Cards) != 0 {
		t.Fatal("unconfirmed local Git commit leaked into preferences", err)
	}
}

func TestPersonalAddressDefaultsRequireAnAuthorizedAddress(t *testing.T) {
	cfg := demoConfig()
	store, _ := OpenStore(context.Background(), cfg)
	user := Identity{Subject: "family"}
	catalog := BuildCatalog(NewDemoSource().Snapshot(), store.Applied(), cfg, user)
	cloud := findCard(t, catalog, "nextcloud")
	initial, _ := store.Favorites(context.Background(), user)
	p := initial.Profile
	p.DefaultEndpoints[cloud.ID] = cloud.Endpoints[1].ID
	next, err := store.SaveFavorites(context.Background(), user, initial.Version, p, catalog)
	if err != nil {
		t.Fatal(err)
	}
	next.Profile.DefaultTarget = "node-node1"
	if _, err := store.SaveFavorites(context.Background(), user, next.Version, next.Profile, catalog); err == nil {
		t.Fatal("unauthorized node default accepted")
	}
	next.Profile.DefaultTarget = ""
	next.Profile.DefaultEndpoints[cloud.ID] = "private-address"
	if _, err := store.SaveFavorites(context.Background(), user, next.Version, next.Profile, catalog); err == nil {
		t.Fatal("unauthorized address default accepted")
	}
	next.Profile.DefaultEndpoints[cloud.ID] = cloud.Endpoints[1].ID
	next.Profile.Collections = append(next.Profile.Collections, FavoriteCollection{ID: "extra", Name: "Extra", Cards: []string{}})
	if _, err := store.SaveFavorites(context.Background(), user, next.Version, next.Profile, catalog); err == nil {
		t.Fatal("ordinary account created multiple collections")
	}
	data, _ := json.Marshal(store.Applied())
	if strings.Contains(string(data), "defaultEndpoint") || strings.Contains(string(data), "defaultTarget") {
		t.Fatal("personal defaults modified common settings")
	}
}
