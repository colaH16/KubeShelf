package shelf

import (
	"encoding/json"
	"strings"
	"testing"

	core "k8s.io/api/core/v1"
	networking "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func demoConfig() Config {
	return Config{Demo: true, PublicURL: "http://localhost:8080", Cluster: "test", IngressSchemes: map[string]string{"demo-https": "https"}}
}
func findCard(t *testing.T, cat Catalog, service string) Card {
	t.Helper()
	for _, c := range cat.Cards {
		for _, e := range c.Endpoints {
			if e.Service == service {
				return c
			}
		}
	}
	t.Fatalf("card %s missing", service)
	return Card{}
}
func TestAuthorizationFiltersBeforeGrouping(t *testing.T) {
	cfg := demoConfig()
	snap := NewDemoSource().Snapshot()
	st := EmptySettings()
	st.Namespaces["public-services"] = Policy{Mode: "public"}
	admin := BuildCatalog(snap, st, cfg, Identity{Admin: true})
	public := findCard(t, admin, "nextcloud")
	private := findCard(t, admin, "rancher")
	st.Assignments[private.Endpoints[0].ID] = public.ID
	cat := BuildCatalog(snap, st, cfg, Identity{})
	b, _ := json.Marshal(cat)
	for _, secret := range []string{"rancher.example.com", "local-console", "192.0.2.12:30002", "new-project"} {
		if strings.Contains(string(b), secret) {
			t.Errorf("unauthorized data leaked: %s", secret)
		}
	}
	if len(findCard(t, cat, "nextcloud").Endpoints) != 2 {
		t.Fatal("same-backend ingress aliases were not grouped")
	}
	if len(cat.Namespaces) != 0 {
		t.Fatal("namespace inbox leaked to anonymous visitor")
	}
}
func TestPerAddressPolicyWinsAndHiddenCannotLeak(t *testing.T) {
	cfg := demoConfig()
	snap := NewDemoSource().Snapshot()
	st := EmptySettings()
	st.Namespaces["public-services"] = Policy{Mode: "public"}
	admin := BuildCatalog(snap, st, cfg, Identity{Admin: true})
	card := findCard(t, admin, "nextcloud")
	private := card.Endpoints[0]
	st.Apps[card.ID] = AppSettings{Endpoints: map[string]EndpointSettings{private.ID: {Visibility: &Policy{Mode: "admin"}}}}
	cat := BuildCatalog(snap, st, cfg, Identity{})
	c := findCard(t, cat, "nextcloud")
	if len(c.Endpoints) != 1 || c.Endpoints[0].ID == private.ID {
		t.Fatal("address policy not enforced")
	}
	st.Apps[card.ID] = AppSettings{Hidden: true}
	b, _ := json.Marshal(BuildCatalog(snap, st, cfg, Identity{}))
	if strings.Contains(string(b), "nextcloud") {
		t.Fatal("hidden service leaked")
	}
}
func TestNodePortLocalOnlyOffersReadyBackendNodes(t *testing.T) {
	cfg := demoConfig()
	snap := NewDemoSource().Snapshot()
	st := EmptySettings()
	st.Targets = []Target{{ID: "domain-unknown", Name: "unmapped", Host: "unmapped.example.com"}, {ID: "domain-node3", Name: "bound", Host: "bound.example.com", NodeName: "node3"}}
	cat := BuildCatalog(snap, st, cfg, Identity{Admin: true})
	e := findCard(t, cat, "local-console").Endpoints[0]
	if !e.Local {
		t.Fatal("Local badge missing")
	}
	want := map[string]bool{"node-node2": true, "node-node3": true, "node-node5": true, "domain-node3": true}
	if len(e.Targets) != len(want) {
		t.Fatalf("wrong targets: %+v", e.Targets)
	}
	for _, target := range e.Targets {
		if !want[target.TargetID] {
			t.Fatalf("ineligible Local target: %s", target.TargetID)
		}
	}
	for i := range snap.Pods {
		if snap.Pods[i].Name == "local-console-node3" {
			snap.Pods[i].Status.Conditions[0].Status = core.ConditionFalse
		}
	}
	e = findCard(t, BuildCatalog(snap, st, cfg, Identity{Admin: true}), "local-console").Endpoints[0]
	for _, target := range e.Targets {
		if target.NodeName == "node3" {
			t.Fatal("Running but not Ready pod remained eligible")
		}
	}
	if e.Health.State != "partial" {
		t.Fatal("partial health missing")
	}
	snap.Connected = false
	e = findCard(t, BuildCatalog(snap, st, cfg, Identity{Admin: true}), "local-console").Endpoints[0]
	if e.Health.State != "unknown" || len(e.Targets) != 0 {
		t.Fatal("stale health remained trusted")
	}
}
func TestNamespaceInboxExcludesEmptyAndReviewIsExplicit(t *testing.T) {
	cfg := demoConfig()
	snap := NewDemoSource().Snapshot()
	st := EmptySettings()
	cat := BuildCatalog(snap, st, cfg, Identity{Admin: true})
	for _, n := range cat.Namespaces {
		if n.Name == "new-project" || n.Services == 0 {
			t.Fatal("namespace without discovered endpoints appeared in the inbox")
		}
	}
	card := findCard(t, cat, "nextcloud")
	if !card.New {
		t.Fatal("new service unmarked")
	}
	st.Apps[card.ID] = AppSettings{Name: "Renamed"}
	if !findCard(t, BuildCatalog(snap, st, cfg, Identity{Admin: true}), "nextcloud").New {
		t.Fatal("cosmetic edit implicitly reviewed service")
	}
	a := st.Apps[card.ID]
	a.Reviewed = map[string]string{}
	for _, e := range card.Endpoints {
		a.Reviewed[e.ID] = e.Fingerprint
	}
	st.Apps[card.ID] = a
	if findCard(t, BuildCatalog(snap, st, cfg, Identity{Admin: true}), "nextcloud").New {
		t.Fatal("review not retained")
	}
	snap.Pods[0].ResourceVersion = "new-rollout"
	if findCard(t, BuildCatalog(snap, st, cfg, Identity{Admin: true}), "nextcloud").Changed {
		t.Fatal("pod update invalidated address review")
	}
}

func TestExistingNamespaceReturnsWhenServicesAreExposed(t *testing.T) {
	for _, kind := range []string{"ingress", "nodeport"} {
		for _, configured := range []bool{false, true} {
			name := kind + "/unconfigured"
			if configured {
				name = kind + "/configured"
			}
			t.Run(name, func(t *testing.T) {
				cfg := demoConfig()
				st := EmptySettings()
				policy := Policy{Mode: "restricted", Groups: []string{"operators"}}
				if configured {
					st.Namespaces["existing"] = policy
				}
				base := Snapshot{Connected: true,
					Namespaces: []core.Namespace{{ObjectMeta: metav1.ObjectMeta{Name: "existing", UID: "same-namespace"}}},
					Services:   []core.Service{{ObjectMeta: metav1.ObjectMeta{Name: "internal", Namespace: "existing"}, Spec: core.ServiceSpec{Type: core.ServiceTypeClusterIP, Ports: []core.ServicePort{{Port: 80}}}}},
				}
				admin := Identity{Admin: true}
				assertEmpty := func() {
					t.Helper()
					cat := BuildCatalog(base, st, cfg, admin)
					if len(cat.Namespaces) != 0 || len(cat.Cards) != 0 {
						t.Fatal("ClusterIP-only namespace should be automatically hidden")
					}
				}
				expose := func(service string) Snapshot {
					snap := base
					svc := base.Services[0].DeepCopy()
					svc.Name = service
					if kind == "nodeport" {
						svc.Spec.Type = core.ServiceTypeNodePort
						svc.Spec.Ports[0].NodePort = 31080
					}
					snap.Services = []core.Service{*svc}
					if kind == "ingress" {
						snap.Ingresses = []networking.Ingress{{ObjectMeta: metav1.ObjectMeta{Name: service, Namespace: "existing"}, Spec: networking.IngressSpec{Rules: []networking.IngressRule{{Host: service + ".example.com", IngressRuleValue: networking.IngressRuleValue{HTTP: &networking.HTTPIngressRuleValue{Paths: []networking.HTTPIngressPath{{Path: "/", Backend: networking.IngressBackend{Service: &networking.IngressServiceBackend{Name: service, Port: networking.ServiceBackendPort{Number: 80}}}}}}}}}}}}
					}
					return snap
				}
				assertDiscovered := func(snap Snapshot, service string) Card {
					t.Helper()
					cat := BuildCatalog(snap, st, cfg, admin)
					if len(cat.Namespaces) != 1 || cat.Namespaces[0].Name != "existing" || cat.Namespaces[0].Services != 1 {
						t.Fatal("existing namespace did not appear when an endpoint was added")
					}
					ns := cat.Namespaces[0]
					if ns.Configured != configured {
						t.Fatal("stored namespace review status changed")
					}
					if configured && (ns.Policy.Mode != policy.Mode || !contains(ns.Policy.Groups, "operators")) {
						t.Fatal("stored visibility policy was lost")
					}
					if !configured && ns.Policy.Mode != "admin" {
						t.Fatal("unconfigured namespace must remain admin-only")
					}
					card := findCard(t, cat, service)
					if !card.New {
						t.Fatal("new endpoint was missing from discovery in an existing namespace")
					}
					return card
				}
				assertEmpty()
				first := expose("first-service")
				card := assertDiscovered(first, "first-service")
				reviewed := map[string]string{}
				for _, e := range card.Endpoints {
					reviewed[e.ID] = e.Fingerprint
				}
				st.Apps[card.ID] = AppSettings{Reviewed: reviewed}
				if findCard(t, BuildCatalog(first, st, cfg, admin), "first-service").New {
					t.Fatal("explicit service review did not clear discovery")
				}
				assertEmpty() // Last endpoint removed; namespace itself still exists.
				assertDiscovered(expose("later-service"), "later-service")
			})
		}
	}
}
func TestPolicyIsFailClosed(t *testing.T) {
	for _, p := range []Policy{{}, {Mode: "restricted"}, {Mode: "admin"}, {Mode: "unknown"}} {
		if p.Allows(Identity{}) {
			t.Fatalf("anonymous policy bypass: %+v", p)
		}
	}
	p := Policy{Mode: "restricted", Groups: []string{"family"}, Users: []string{"user-id"}}
	if !p.Allows(Identity{Subject: "a", Groups: []string{"family"}}) || !p.Allows(Identity{Subject: "user-id"}) || p.Allows(Identity{Subject: "other"}) {
		t.Fatal("group/user OR policy mismatch")
	}
}

func TestHiddenGroupedNodePortsDoNotExposeTargets(t *testing.T) {
	cfg := demoConfig()
	snap := NewDemoSource().Snapshot()
	st := EmptySettings()
	admin := BuildCatalog(snap, st, cfg, Identity{Admin: true})
	for _, c := range admin.Cards {
		if c.Source == "nodeport" {
			st.Apps[c.ID] = AppSettings{Visibility: &Policy{Mode: "public"}}
			for _, e := range c.Endpoints {
				st.Assignments[e.ID] = "hidden-group"
			}
		}
	}
	st.Apps["hidden-group"] = AppSettings{Hidden: true}
	cat := BuildCatalog(snap, st, cfg, Identity{})
	if len(cat.Targets) != 0 {
		t.Fatal("hidden NodePort group exposed node targets")
	}
}

func TestDefaultBackendRequiresConcreteURL(t *testing.T) {
	cfg := demoConfig()
	snap := NewDemoSource().Snapshot()
	ing := snap.Ingresses[0].DeepCopy()
	ing.Spec.DefaultBackend = &ing.Spec.Rules[0].HTTP.Paths[0].Backend
	ing.Spec.Rules = nil
	snap.Ingresses = []networking.Ingress{*ing}
	cat := BuildCatalog(snap, EmptySettings(), cfg, Identity{Admin: true})
	found := false
	for _, c := range cat.Cards {
		for _, e := range c.Endpoints {
			if e.Kind == "ingress" {
				found = true
				if e.URL != "" || !e.NeedsURL {
					t.Fatal("default backend generated an unsafe URL")
				}
			}
		}
	}
	if !found {
		t.Fatal("default backend was omitted")
	}
}
