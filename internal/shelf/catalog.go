package shelf

import (
	"net"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"strings"

	core "k8s.io/api/core/v1"
	discovery "k8s.io/api/discovery/v1"
	networking "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/labels"
)

func key(ns, n string) string { return ns + "/" + n }
func podReady(p core.Pod) bool {
	if p.DeletionTimestamp != nil || p.Status.Phase != core.PodRunning {
		return false
	}
	for _, c := range p.Status.Conditions {
		if c.Type == core.PodReady {
			return c.Status == core.ConditionTrue
		}
	}
	return false
}
func nodeReady(n core.Node) bool {
	for _, c := range n.Status.Conditions {
		if c.Type == core.NodeReady {
			return c.Status == core.ConditionTrue
		}
	}
	return false
}
func serviceHealth(snap Snapshot, svc core.Service, port core.ServicePort) Health {
	h := Health{State: "unknown", Reason: "Pod 상태를 판단할 수 없습니다", ReadyNodes: []string{}}
	pods := map[string]core.Pod{}
	total := map[string]bool{}
	ready := map[string]bool{}
	external := svc.Spec.Type == core.ServiceTypeExternalName
	for _, p := range snap.Pods {
		pods[key(p.Namespace, p.Name)] = p
		if p.Namespace == svc.Namespace && len(svc.Spec.Selector) > 0 && labels.SelectorFromSet(svc.Spec.Selector).Matches(labels.Set(p.Labels)) && p.Status.Phase != core.PodSucceeded && p.Status.Phase != core.PodFailed {
			total[key(p.Namespace, p.Name)] = true
		}
	}
	for _, slice := range snap.Slices {
		if slice.Namespace != svc.Namespace || slice.Labels[discovery.LabelServiceName] != svc.Name {
			continue
		}
		matches := false
		for _, p := range slice.Ports {
			if (p.Name == nil && port.Name == "" || p.Name != nil && *p.Name == port.Name) && (p.Protocol == nil || *p.Protocol == port.Protocol || port.Protocol == "") {
				matches = true
			}
		}
		if !matches {
			continue
		}
		for _, ep := range slice.Endpoints {
			if ep.TargetRef == nil || ep.TargetRef.Kind != "Pod" {
				external = true
				continue
			}
			ns := ep.TargetRef.Namespace
			if ns == "" {
				ns = svc.Namespace
			}
			id := key(ns, ep.TargetRef.Name)
			p, ok := pods[id]
			total[id] = true
			if ok && podReady(p) && (ep.Conditions.Ready == nil || *ep.Conditions.Ready) && (ep.Conditions.Terminating == nil || !*ep.Conditions.Terminating) {
				ready[id] = true
				if p.Spec.NodeName != "" {
					h.ReadyNodes = append(h.ReadyNodes, p.Spec.NodeName)
				}
			}
		}
	}
	h.Ready = len(ready)
	h.Total = len(total)
	h.ReadyNodes = sortedUnique(h.ReadyNodes)
	switch {
	case !snap.Connected:
		h.Reason = "Kubernetes 상태를 확인할 수 없습니다"
		h.ReadyNodes = []string{}
	case h.Total == 0 && external:
		h.Reason = "외부 백엔드 · Pod 상태 확인 대상 아님"
	case h.Ready == 0:
		h.State = "down"
		h.Reason = "준비된 Pod 없음"
	case h.Ready < h.Total:
		h.State = "partial"
		h.Reason = "일부 Pod 준비 중"
	default:
		h.State = "healthy"
		h.Reason = "Pod Ready"
	}
	return h
}
func schemeForPort(p core.ServicePort) string {
	if p.Protocol != core.ProtocolTCP && p.Protocol != "" {
		return "tcp"
	}
	v := strings.ToLower(p.Name)
	if p.AppProtocol != nil {
		v += " " + strings.ToLower(*p.AppProtocol)
	}
	if strings.Contains(v, "https") {
		return "https"
	}
	if strings.Contains(v, "http") {
		return "http"
	}
	return "tcp"
}
func endpointFingerprint(e Endpoint) string {
	return stableID(e.ID, e.URL, e.Scheme, strconv.Itoa(int(e.NodePort)), e.Protocol)
}
func endpointPolicy(s Settings, e Endpoint) Policy {
	a := s.Apps[e.AppID]
	if p := a.Endpoints[e.ID].Visibility; p != nil {
		return *p
	}
	if a.Visibility != nil {
		return *a.Visibility
	}
	if p, ok := s.Namespaces[e.Namespace]; ok && e.Namespace != "" {
		return p
	}
	return Policy{Mode: "admin"}
}
func BuildCatalog(snap Snapshot, s Settings, cfg Config, user Identity) Catalog {
	out := Catalog{Cards: []Card{}, Targets: []Target{}, Connected: snap.Connected, UpdatedAt: snap.UpdatedAt, Identity: user, Demo: cfg.Demo, AppliedRevision: s.Revision}
	if user.Admin {
		out.Problem = snap.Problem
	}
	svcs := map[string]core.Service{}
	for _, svc := range snap.Services {
		svcs[key(svc.Namespace, svc.Name)] = svc
	}
	endpoints := map[string]Endpoint{}
	for _, ing := range snap.Ingresses {
		rules := append([]networking.IngressRule{}, ing.Spec.Rules...)
		if ing.Spec.DefaultBackend != nil {
			rules = append(rules, networking.IngressRule{IngressRuleValue: networking.IngressRuleValue{HTTP: &networking.HTTPIngressRuleValue{Paths: []networking.HTTPIngressPath{{Path: "/", Backend: *ing.Spec.DefaultBackend}}}}})
		}
		for _, rule := range rules {
			if rule.HTTP == nil {
				continue
			}
			for _, path := range rule.HTTP.Paths {
				if path.Backend.Service == nil {
					continue
				}
				back := path.Backend.Service
				svc, ok := svcs[key(ing.Namespace, back.Name)]
				p := core.ServicePort{Port: back.Port.Number, Name: back.Port.Name}
				if ok {
					for _, candidate := range svc.Spec.Ports {
						if (back.Port.Name != "" && back.Port.Name == candidate.Name) || (back.Port.Number != 0 && back.Port.Number == candidate.Port) {
							p = candidate
							break
						}
					}
				}
				port := strconv.Itoa(int(p.Port))
				if p.Port == 0 {
					port = p.Name
				}
				id := "endpoint-" + stableID("ingress", ing.Namespace, back.Name, port, rule.Host, path.Path)
				scheme := "http"
				for _, tls := range ing.Spec.TLS {
					if contains(tls.Hosts, rule.Host) {
						scheme = "https"
					}
				}
				if ing.Spec.IngressClassName != nil {
					if v := cfg.IngressSchemes[*ing.Spec.IngressClassName]; v != "" {
						scheme = v
					}
				}
				route := path.Path
				if route == "" {
					route = "/"
				}
				needs := rule.Host == "" || strings.Contains(rule.Host, "*") || strings.ContainsAny(route, "*()[]{}|^$")
				e := Endpoint{ID: id, AppID: "auto-" + stableID("ingress", cfg.Cluster, ing.Namespace, back.Name, port), Label: rule.Host + route, Kind: "ingress", Namespace: ing.Namespace, Service: back.Name, Port: port, Scheme: scheme, NeedsURL: needs, Targets: []Connection{}, Health: Health{State: "unknown", Reason: "Service를 찾을 수 없습니다"}}
				if !needs {
					e.URL = (&url.URL{Scheme: scheme, Host: rule.Host, Path: route}).String()
				}
				if ok {
					e.Health = serviceHealth(snap, svc, p)
				}
				endpoints[id] = e
			}
		}
	}
	for _, svc := range snap.Services {
		for _, p := range svc.Spec.Ports {
			if p.NodePort == 0 {
				continue
			}
			port := strconv.Itoa(int(p.Port))
			id := "endpoint-" + stableID("nodeport", svc.Namespace, svc.Name, port)
			endpoints[id] = Endpoint{ID: id, AppID: "auto-" + stableID("nodeport", cfg.Cluster, svc.Namespace, svc.Name, port), Label: svc.Name, Kind: "nodeport", Namespace: svc.Namespace, Service: svc.Name, Port: port, NodePort: p.NodePort, Scheme: schemeForPort(p), Protocol: string(p.Protocol), Local: svc.Spec.ExternalTrafficPolicy == core.ServiceExternalTrafficPolicyLocal, Health: serviceHealth(snap, svc, p), Targets: []Connection{}}
		}
	}
	for _, m := range s.Manual {
		for _, u := range m.URLs {
			parsed, _ := url.Parse(u.URL)
			scheme := "https"
			if parsed != nil {
				scheme = parsed.Scheme
			}
			endpoints[u.ID] = Endpoint{ID: u.ID, AppID: m.ID, Label: u.Label, URL: u.URL, Kind: "custom", Service: m.Name, Scheme: scheme, Health: Health{State: "unknown", Reason: "수동 등록 서비스"}, Targets: []Connection{}}
		}
	}
	targets := []Target{}
	for _, n := range snap.Nodes {
		if !nodeReady(n) {
			continue
		}
		host := ""
		for _, a := range n.Status.Addresses {
			if a.Type == core.NodeInternalIP {
				host = a.Address
				break
			}
		}
		if host == "" {
			for _, a := range n.Status.Addresses {
				if a.Type == core.NodeExternalIP {
					host = a.Address
					break
				}
			}
		}
		if host != "" {
			targets = append(targets, Target{ID: "node-" + n.Name, Name: n.Name, Host: host, NodeName: n.Name})
		}
	}
	for _, t := range s.Targets {
		p := Policy{Mode: "admin"}
		if t.Visibility != nil {
			p = *t.Visibility
		}
		if p.Allows(user) {
			t.Manual = true
			targets = append(targets, t)
		}
	}
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].Manual != targets[j].Manual {
			return !targets[i].Manual
		}
		return targets[i].Name < targets[j].Name
	})
	usedTargets := map[string]bool{}
	cards := map[string]*Card{}
	namespaceServices := map[string]map[string]bool{}
	ids := make([]string, 0, len(endpoints))
	for id := range endpoints {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		e := endpoints[id]
		if namespaceServices[e.Namespace] == nil {
			namespaceServices[e.Namespace] = map[string]bool{}
		}
		namespaceServices[e.Namespace][e.AppID] = true
		a := s.Apps[e.AppID]
		if !endpointPolicy(s, e).Allows(user) || a.Hidden && !user.Admin {
			continue
		}
		eo := a.Endpoints[e.ID]
		if eo.Label != "" {
			e.Label = eo.Label
		}
		if eo.Scheme != "" {
			e.Scheme = eo.Scheme
		}
		if eo.URL != "" {
			e.URL = eo.URL
			e.NeedsURL = false
		} else if e.URL != "" {
			u, _ := url.Parse(e.URL)
			if u != nil && e.Scheme != "tcp" {
				u.Scheme = e.Scheme
				e.URL = u.String()
			}
		}
		if e.Kind == "nodeport" {
			for _, t := range targets {
				if e.Local && (t.NodeName == "" || !contains(e.Health.ReadyNodes, t.NodeName)) {
					continue
				}
				address := net.JoinHostPort(t.Host, strconv.Itoa(int(e.NodePort)))
				u := ""
				if e.Scheme == "http" || e.Scheme == "https" {
					u = e.Scheme + "://" + address
				}
				if e.URL != "" {
					u = e.URL
				}
				e.Targets = append(e.Targets, Connection{TargetID: t.ID, Name: t.Name, URL: u, Address: address, NodeName: t.NodeName})
			}
		}
		e.Fingerprint = endpointFingerprint(e)
		group := e.AppID
		if assigned := s.Assignments[e.ID]; assigned != "" {
			group = assigned
		}
		meta := s.Apps[group]
		// Authorization above always uses the original endpoint policy, before presentation grouping.
		groupKey := e.Kind + "/" + group
		card := cards[groupKey]
		if card == nil {
			name := meta.Name
			if name == "" {
				name = a.Name
			}
			if name == "" {
				name = e.Service
			}
			card = &Card{ID: group, Name: name, Icon: meta.Icon, Description: meta.Description, Source: e.Kind, Namespace: e.Namespace, Hidden: a.Hidden || meta.Hidden, Endpoints: []Endpoint{}}
			if card.Icon == "" {
				card.Icon = a.Icon
			}
			if card.Description == "" {
				card.Description = a.Description
			}
			cards[groupKey] = card
		}
		if card.Hidden && !user.Admin {
			continue
		}
		for _, t := range e.Targets {
			usedTargets[t.TargetID] = true
		}
		if e.Kind != "custom" {
			prior, reviewed := a.Reviewed[e.ID]
			if !reviewed {
				card.New = true
			} else if prior != e.Fingerprint {
				card.Changed = true
			}
		}
		if !user.Admin {
			e.Fingerprint = ""
			e.Health.ReadyNodes = nil
		}
		card.Endpoints = append(card.Endpoints, e)
	}
	for _, card := range cards {
		if len(card.Endpoints) > 0 {
			out.Cards = append(out.Cards, *card)
		}
	}
	sort.Slice(out.Cards, func(i, j int) bool { return strings.ToLower(out.Cards[i].Name) < strings.ToLower(out.Cards[j].Name) })
	for _, t := range targets {
		if user.Admin || usedTargets[t.ID] {
			t.Visibility = nil
			out.Targets = append(out.Targets, t)
		}
	}
	if user.Admin {
		out.Namespaces = []NamespaceInfo{}
		for _, n := range snap.Namespaces {
			// Namespace visibility follows discovered endpoints, not namespace age.
			// Keep stored policies intact while a namespace has no exposed services.
			if len(namespaceServices[n.Name]) == 0 {
				continue
			}
			p, ok := s.Namespaces[n.Name]
			if !ok {
				p = Policy{Mode: "admin"}
			}
			out.Namespaces = append(out.Namespaces, NamespaceInfo{Name: n.Name, Configured: ok, Services: len(namespaceServices[n.Name]), Policy: p})
		}
		sort.Slice(out.Namespaces, func(i, j int) bool { return out.Namespaces[i].Name < out.Namespaces[j].Name })
	}
	return out
}
func changeSummary(old, next Settings) string {
	parts := []string{}
	for ns, p := range next.Namespaces {
		before, ok := old.Namespaces[ns]
		if !ok || !reflect.DeepEqual(before, p) {
			parts = append(parts, "namespace "+ns+" visibility")
		}
	}
	for id, a := range next.Apps {
		before, ok := old.Apps[id]
		fields := []string{}
		if !ok || a.Name != before.Name {
			fields = append(fields, "name")
		}
		if a.Icon != before.Icon {
			fields = append(fields, "icon")
		}
		if a.Description != before.Description {
			fields = append(fields, "description")
		}
		if a.Hidden != before.Hidden {
			fields = append(fields, "hidden")
		}
		if !reflect.DeepEqual(a.Visibility, before.Visibility) {
			fields = append(fields, "visibility")
		}
		if !reflect.DeepEqual(a.Endpoints, before.Endpoints) {
			fields = append(fields, "addresses")
		}
		if !reflect.DeepEqual(a.Reviewed, before.Reviewed) {
			fields = append(fields, "review")
		}
		if len(fields) > 0 {
			name := a.Name
			if name == "" {
				name = id
			}
			parts = append(parts, name+" ("+strings.Join(fields, ", ")+")")
		}
	}
	if !reflect.DeepEqual(old.Manual, next.Manual) {
		parts = append(parts, "manual services")
	}
	if !reflect.DeepEqual(old.Targets, next.Targets) {
		parts = append(parts, "node domains")
	}
	if !reflect.DeepEqual(old.Assignments, next.Assignments) {
		parts = append(parts, "card grouping")
	}
	if len(parts) == 0 {
		parts = append(parts, "settings")
	}
	sort.Strings(parts)
	msg := "Update " + strings.Join(parts, "; ")
	msg = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return ' '
		}
		return r
	}, msg)
	if len(msg) > 1000 {
		msg = msg[:1000]
	}
	return msg
}
