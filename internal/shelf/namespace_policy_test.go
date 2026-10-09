package shelf

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLegacyPublicNamespaceDoesNotExposeNodePorts(t *testing.T) {
	var settings Settings
	if err := json.Unmarshal([]byte(`{"schemaVersion":1,"revision":"legacy","namespaces":{"public-services":{"mode":"public"}}}`), &settings); err != nil {
		t.Fatal(err)
	}
	if err := ValidateSettings(&settings); err != nil {
		t.Fatal(err)
	}
	cat := BuildCatalog(NewDemoSource().Snapshot(), settings, demoConfig(), Identity{})
	findCard(t, cat, "nextcloud")
	if len(cat.Targets) != 0 {
		t.Fatal("legacy Ingress policy exposed node targets")
	}
	for _, c := range cat.Cards {
		if c.Source == "nodeport" {
			t.Fatal("NodePort inherited the legacy public namespace policy")
		}
	}
	raw, _ := json.Marshal(cat)
	if strings.Contains(string(raw), "192.0.2.") || strings.Contains(string(raw), "30001") {
		t.Fatal("node address or NodePort leaked in an anonymous catalog")
	}
}

func TestNamespaceSourcePoliciesAreIndependent(t *testing.T) {
	st := EmptySettings()
	snap := NewDemoSource().Snapshot()
	st.Namespaces["public-services"] = Policy{Mode: "public"}
	st.NodePortNamespaces["public-services"] = Policy{Mode: "restricted", Groups: []string{"operators"}}
	for _, user := range []Identity{{}, {Subject: "family", Groups: []string{"family"}}} {
		cat := BuildCatalog(snap, st, demoConfig(), user)
		findCard(t, cat, "nextcloud")
		if len(cat.Targets) != 0 {
			t.Fatal("NodePort group policy did not filter node targets")
		}
	}
	allowed := Identity{Subject: "operator", Groups: []string{"operators"}}
	cat := BuildCatalog(snap, st, demoConfig(), allowed)
	nodeport := findCard(t, cat, "uptime-kuma")
	if len(cat.Targets) == 0 || len(nodeport.Endpoints[0].Targets) == 0 {
		t.Fatal("allowed NodePort user has no connection targets")
	}
	st.Namespaces["public-services"] = Policy{Mode: "admin"}
	st.NodePortNamespaces["public-services"] = Policy{Mode: "public"}
	cat = BuildCatalog(snap, st, demoConfig(), Identity{})
	if len(cat.Cards) != 1 || cat.Cards[0].Source != "nodeport" {
		t.Fatal("NodePort namespace policy changed Ingress visibility")
	}
	// Explicit per-service and per-address settings retain their precedence.
	st.Apps[nodeport.ID] = AppSettings{Visibility: &Policy{Mode: "admin"}}
	if len(BuildCatalog(snap, st, demoConfig(), Identity{}).Targets) != 0 {
		t.Fatal("service override failed to hide NodePort targets")
	}
	st.Apps[nodeport.ID] = AppSettings{Visibility: &Policy{Mode: "admin"}, Endpoints: map[string]EndpointSettings{nodeport.Endpoints[0].ID: {Visibility: &Policy{Mode: "public"}}}}
	findCard(t, BuildCatalog(snap, st, demoConfig(), Identity{}), "uptime-kuma")
}

func TestNamespaceReviewTracksEachExposedKind(t *testing.T) {
	st := EmptySettings()
	st.Namespaces["public-services"] = Policy{Mode: "public"}
	snap := NewDemoSource().Snapshot()
	namespace := func() NamespaceInfo {
		for _, n := range BuildCatalog(snap, st, demoConfig(), Identity{Admin: true}).Namespaces {
			if n.Name == "public-services" {
				return n
			}
		}
		t.Fatal("namespace missing")
		return NamespaceInfo{}
	}
	n := namespace()
	if n.Configured || !n.IngressConfigured || n.NodePortConfigured || n.IngressServices != 2 || n.NodePortServices != 1 || n.NodePortPolicy.Mode != "admin" {
		t.Fatalf("new NodePort policy was not independently flagged: %+v", n)
	}
	st.NodePortNamespaces["public-services"] = Policy{Mode: "admin"}
	if !namespace().Configured {
		t.Fatal("explicit NodePort review did not clear the namespace inbox")
	}
	if !strings.Contains(changeSummary(EmptySettings(), st), "namespace public-services NodePort visibility") {
		t.Fatal("NodePort change is missing from the Git commit summary")
	}
	delete(st.NodePortNamespaces, "public-services")
	for i := range snap.Services {
		if snap.Services[i].Namespace == "public-services" {
			for p := range snap.Services[i].Spec.Ports {
				snap.Services[i].Spec.Ports[p].NodePort = 0
			}
		}
	}
	if !namespace().Configured {
		t.Fatal("Ingress-only namespace unnecessarily requires a NodePort review")
	}
	st.NodePortNamespaces["public-services"] = Policy{Mode: "unsupported"}
	if ValidateSettings(&st) == nil {
		t.Fatal("invalid NodePort policy was accepted")
	}
}
