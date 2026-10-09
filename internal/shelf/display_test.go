package shelf

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNamespaceHidingIsIndependentAndRetainsNamespace(t *testing.T) {
	s := EmptySettings()
	snap := NewDemoSource().Snapshot()
	cfg := demoConfig()
	s.Namespaces["public-services"] = Policy{Mode: "public", Hidden: true}
	s.NodePortNamespaces["public-services"] = Policy{Mode: "public"}
	anon := BuildCatalog(snap, s, cfg, Identity{})
	for _, c := range anon.Cards {
		if c.Source == "ingress" {
			t.Fatal("hidden Ingress exposed")
		}
	}
	findCard(t, anon, "uptime-kuma")
	admin := BuildCatalog(snap, s, cfg, Identity{Admin: true})
	cloud := findCard(t, admin, "nextcloud")
	if !cloud.Hidden || !cloud.Endpoints[0].Hidden {
		t.Fatal("admin hidden view metadata missing")
	}
	found := false
	for _, n := range admin.Namespaces {
		if n.Name == "public-services" {
			found = true
			if !n.Policy.Hidden || n.NodePortPolicy.Hidden || n.IngressServices == 0 {
				t.Fatal("namespace defaults not retained independently")
			}
		}
	}
	if !found {
		t.Fatal("hiding services removed namespace")
	}
	s.Namespaces["public-services"] = Policy{Mode: "public"}
	s.NodePortNamespaces["public-services"] = Policy{Mode: "public", Hidden: true}
	anon = BuildCatalog(snap, s, cfg, Identity{})
	findCard(t, anon, "nextcloud")
	if len(anon.Targets) != 0 {
		t.Fatal("hidden NodePort leaked node targets")
	}
	raw, _ := json.Marshal(anon)
	if strings.Contains(string(raw), "30001") || strings.Contains(string(raw), "192.0.2.") {
		t.Fatal("hidden node address leaked")
	}
}

func TestServiceDisplayInheritanceAndLegacySettings(t *testing.T) {
	s := EmptySettings()
	snap := NewDemoSource().Snapshot()
	cfg := demoConfig()
	s.Namespaces["public-services"] = Policy{Mode: "public", Hidden: true}
	cloud := findCard(t, BuildCatalog(snap, s, cfg, Identity{Admin: true}), "nextcloud")
	for _, tc := range []struct {
		name   string
		app    AppSettings
		hidden bool
	}{
		{"legacy false inherits", AppSettings{Hidden: false}, true},
		{"explicit inherit", AppSettings{Display: "inherit"}, true},
		{"explicit show", AppSettings{Display: "show"}, false},
		{"explicit hide", AppSettings{Display: "hide"}, true},
		{"legacy hide", AppSettings{Hidden: true}, true},
		{"explicit choice supersedes legacy", AppSettings{Hidden: true, Display: "show"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s.Apps[cloud.ID] = tc.app
			c := findCard(t, BuildCatalog(snap, s, cfg, Identity{Admin: true}), "nextcloud")
			if c.Hidden != tc.hidden {
				t.Fatalf("hidden = %v", c.Hidden)
			}
		})
	}
	s.Apps[cloud.ID] = AppSettings{Display: "show", Visibility: &Policy{Mode: "admin"}}
	for _, c := range BuildCatalog(snap, s, cfg, Identity{}).Cards {
		if c.ID == cloud.ID {
			t.Fatal("show bypassed service permissions")
		}
	}
	s.Namespaces["public-services"] = Policy{Mode: "admin", Hidden: true}
	s.Apps[cloud.ID] = AppSettings{Display: "show"}
	if len(BuildCatalog(snap, s, cfg, Identity{}).Cards) != 0 {
		t.Fatal("show bypassed namespace permissions")
	}
	s.Apps[cloud.ID] = AppSettings{Display: "unsupported"}
	if ValidateSettings(&s) == nil {
		t.Fatal("invalid display mode accepted")
	}
}

func TestGroupedAddressesKeepTheirOwnHiddenDefaults(t *testing.T) {
	s := EmptySettings()
	snap := NewDemoSource().Snapshot()
	cfg := demoConfig()
	s.Namespaces["public-services"] = Policy{Mode: "public", Hidden: true}
	s.Namespaces["infra"] = Policy{Mode: "public"}
	initial := BuildCatalog(snap, s, cfg, Identity{Admin: true})
	cloud := findCard(t, initial, "nextcloud")
	grafana := findCard(t, initial, "grafana")
	for _, c := range []Card{cloud, grafana} {
		for _, e := range c.Endpoints {
			s.Assignments[e.ID] = "mixed"
		}
	}
	s.Apps["mixed"] = AppSettings{Name: "Mixed", Display: "show"}
	mixed := findCard(t, BuildCatalog(snap, s, cfg, Identity{Admin: true}), "grafana")
	if mixed.Hidden {
		t.Fatal("one hidden namespace hid whole mixed card")
	}
	hidden, shown := 0, 0
	for _, e := range mixed.Endpoints {
		if e.Hidden {
			hidden++
		} else {
			shown++
		}
	}
	if hidden != len(cloud.Endpoints) || shown != len(grafana.Endpoints) {
		t.Fatalf("unexpected split: hidden %d shown %d", hidden, shown)
	}
	mixed = findCard(t, BuildCatalog(snap, s, cfg, Identity{}), "grafana")
	for _, e := range mixed.Endpoints {
		if e.Namespace == "public-services" {
			t.Fatal("grouping exposed hidden namespace")
		}
	}
	s.Apps["mixed"] = AppSettings{Name: "Mixed", Hidden: true}
	for _, c := range BuildCatalog(snap, s, cfg, Identity{}).Cards {
		if c.ID == "mixed" {
			t.Fatal("legacy group hiding lost")
		}
	}
}

func TestDisplaySettingsRoundTripAndCommitSummary(t *testing.T) {
	before := EmptySettings()
	after := cloneSettings(before)
	after.Namespaces["public-services"] = Policy{Mode: "public", Hidden: true}
	after.Apps["app"] = AppSettings{Display: "show"}
	if err := ValidateSettings(&after); err != nil {
		t.Fatal(err)
	}
	cloned := cloneSettings(after)
	if !cloned.Namespaces["public-services"].Hidden || cloned.Apps["app"].Display != "show" {
		t.Fatal("display settings not preserved")
	}
	msg := changeSummary(before, after)
	if !strings.Contains(msg, "namespace public-services Ingress visibility") || !strings.Contains(msg, "hidden") {
		t.Fatalf("display changes absent from commit: %s", msg)
	}
}
