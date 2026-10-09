package shelf

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestDefaultReferencesNeverBypassVisibility(t *testing.T) {
	cfg, snap, st := demoConfig(), NewDemoSource().Snapshot(), EmptySettings()
	st.Namespaces["public-services"] = Policy{Mode: "public"}
	admin := BuildCatalog(snap, st, cfg, Identity{Admin: true})
	card := findCard(t, admin, "nextcloud")
	selected := card.Endpoints[1].ID
	st.DefaultTarget = "node-node3"
	st.Apps[card.ID] = AppSettings{DefaultEndpoint: selected}
	public := BuildCatalog(snap, st, cfg, Identity{})
	if findCard(t, public, "nextcloud").DefaultEndpoint != selected {
		t.Fatal("public default missing")
	}
	if public.DefaultTarget != "" || len(public.Targets) != 0 {
		t.Fatal("private node target leaked through default")
	}
	a := st.Apps[card.ID]
	a.Endpoints = map[string]EndpointSettings{selected: {Visibility: &Policy{Mode: "admin"}}}
	st.Apps[card.ID] = a
	public = BuildCatalog(snap, st, cfg, Identity{})
	if findCard(t, public, "nextcloud").DefaultEndpoint != "" {
		t.Fatal("unauthorized default endpoint leaked")
	}
	if findCard(t, BuildCatalog(snap, st, cfg, Identity{Admin: true}), "nextcloud").DefaultEndpoint != selected {
		t.Fatal("admin lost default")
	}
	a.DefaultEndpoint = "removed-endpoint"
	st.Apps[card.ID] = a
	st.DefaultTarget = "removed-node"
	admin = BuildCatalog(snap, st, cfg, Identity{Admin: true})
	if admin.DefaultTarget != "" || findCard(t, admin, "nextcloud").DefaultEndpoint != "" {
		t.Fatal("missing defaults must fall back to available addresses")
	}
}

func TestDefaultChoicesSurviveGitAndMountedConfig(t *testing.T) {
	store, remote, _ := newTestStore(t)
	next, base := store.Desired()
	next.DefaultTarget = "node-node3"
	next.Apps["nextcloud"] = AppSettings{DefaultEndpoint: "endpoint-files", Visibility: &Policy{Mode: "admin"}}
	status, err := store.Save(context.Background(), base, next, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if !status.Pending || store.Applied().DefaultTarget != "" {
		t.Fatal("choices bypassed mounted configuration")
	}
	message := gitTest(t, remote, "log", "-1", "--format=%B")
	if !strings.Contains(message, "default address") || !strings.Contains(message, "default NodePort target") {
		t.Fatal("commit omitted default changes", message)
	}
	if err := store.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	persisted, _ := store.Desired()
	if persisted.DefaultTarget != next.DefaultTarget || persisted.Apps["nextcloud"].DefaultEndpoint != "endpoint-files" {
		t.Fatal("Git round trip lost choices")
	}
	data, _ := json.Marshal(persisted)
	if err := os.WriteFile(store.cfg.SettingsFile, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := store.loadApplied(); err != nil {
		t.Fatal(err)
	}
	if store.Status().Pending || store.Applied().DefaultTarget != next.DefaultTarget {
		t.Fatal("mounted defaults were not applied")
	}
}
