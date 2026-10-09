package shelf

import (
	"context"
	"sync"
	"time"

	core "k8s.io/api/core/v1"
	discovery "k8s.io/api/discovery/v1"
	networking "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

type KubernetesSource struct {
	mu       sync.RWMutex
	snapshot Snapshot
	client   kubernetes.Interface
}

func NewKubernetesSource(ctx context.Context, c Config) (*KubernetesSource, error) {
	var rc *rest.Config
	var err error
	if c.Kubeconfig != "" {
		rc, err = clientcmd.BuildConfigFromFlags("", c.Kubeconfig)
	} else {
		rc, err = rest.InClusterConfig()
	}
	if err != nil {
		return nil, err
	}
	rc.Timeout = 10 * time.Second
	rc.UserAgent = "KubeShelf"
	client, err := kubernetes.NewForConfig(rc)
	if err != nil {
		return nil, err
	}
	s := &KubernetesSource{client: client}
	s.refresh(ctx)
	go func() {
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.refresh(ctx)
			}
		}
	}()
	return s, nil
}
func (s *KubernetesSource) Snapshot() Snapshot { s.mu.RLock(); defer s.mu.RUnlock(); return s.snapshot }
func (s *KubernetesSource) refresh(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	var next Snapshot
	var wg sync.WaitGroup
	errs := make(chan error, 6)
	opts := metav1.ListOptions{ResourceVersion: "0"}
	jobs := []func() error{
		func() error {
			v, e := s.client.CoreV1().Namespaces().List(ctx, opts)
			if e == nil {
				next.Namespaces = v.Items
			}
			return e
		},
		func() error {
			v, e := s.client.CoreV1().Services("").List(ctx, opts)
			if e == nil {
				next.Services = v.Items
			}
			return e
		},
		func() error {
			v, e := s.client.CoreV1().Pods("").List(ctx, opts)
			if e == nil {
				next.Pods = v.Items
			}
			return e
		},
		func() error {
			v, e := s.client.CoreV1().Nodes().List(ctx, opts)
			if e == nil {
				next.Nodes = v.Items
			}
			return e
		},
		func() error {
			v, e := s.client.DiscoveryV1().EndpointSlices("").List(ctx, opts)
			if e == nil {
				next.Slices = v.Items
			}
			return e
		},
		func() error {
			v, e := s.client.NetworkingV1().Ingresses("").List(ctx, opts)
			if e == nil {
				next.Ingresses = v.Items
			}
			return e
		},
	}
	for _, job := range jobs {
		wg.Add(1)
		go func(f func() error) { defer wg.Done(); errs <- f() }(job)
	}
	wg.Wait()
	close(errs)
	s.mu.Lock()
	defer s.mu.Unlock()
	for e := range errs {
		if e != nil {
			s.snapshot.Connected = false
			s.snapshot.Problem = "Kubernetes 동기화가 지연되고 있습니다"
			return
		}
	}
	next.Connected = true
	next.UpdatedAt = time.Now().UTC()
	s.snapshot = next
}

type DemoSource struct{ snapshot Snapshot }

func (d *DemoSource) Snapshot() Snapshot { return d.snapshot }
func NewDemoSource() *DemoSource {
	s := Snapshot{Connected: true, UpdatedAt: time.Now().UTC()}
	for _, ns := range []string{"public-services", "myapps", "infra", "new-project"} {
		s.Namespaces = append(s.Namespaces, core.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}})
	}
	for i, n := range []string{"node1", "node2", "node3", "node4", "node5"} {
		s.Nodes = append(s.Nodes, core.Node{ObjectMeta: metav1.ObjectMeta{Name: n}, Status: core.NodeStatus{Addresses: []core.NodeAddress{{Type: core.NodeInternalIP, Address: []string{"192.0.2.11", "192.0.2.12", "192.0.2.13", "192.0.2.14", "192.0.2.15"}[i]}}, Conditions: []core.NodeCondition{{Type: core.NodeReady, Status: core.ConditionTrue}}}})
	}
	add := func(ns, name, host string, nodeport int32, local bool, nodes []string) {
		service := core.Service{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}, Spec: core.ServiceSpec{Selector: map[string]string{"app": name}, Ports: []core.ServicePort{{Name: "http", Port: 80, TargetPort: intstr.FromInt32(8080), Protocol: core.ProtocolTCP, NodePort: nodeport}}}}
		if nodeport != 0 {
			service.Spec.Type = core.ServiceTypeNodePort
		}
		if local {
			service.Spec.ExternalTrafficPolicy = core.ServiceExternalTrafficPolicyLocal
		}
		s.Services = append(s.Services, service)
		if host != "" {
			cl := "demo-https"
			ing := networking.Ingress{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}, Spec: networking.IngressSpec{IngressClassName: &cl, Rules: []networking.IngressRule{{Host: host, IngressRuleValue: networking.IngressRuleValue{HTTP: &networking.HTTPIngressRuleValue{Paths: []networking.HTTPIngressPath{{Path: "/", Backend: networking.IngressBackend{Service: &networking.IngressServiceBackend{Name: name, Port: networking.ServiceBackendPort{Number: 80}}}}}}}}}}}
			s.Ingresses = append(s.Ingresses, ing)
		}
		pn := "http"
		port := int32(8080)
		slice := discovery.EndpointSlice{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Labels: map[string]string{discovery.LabelServiceName: name}}, Ports: []discovery.EndpointPort{{Name: &pn, Port: &port}}}
		for _, node := range nodes {
			ready := true
			podname := name + "-" + node
			s.Pods = append(s.Pods, core.Pod{ObjectMeta: metav1.ObjectMeta{Name: podname, Namespace: ns, Labels: map[string]string{"app": name}}, Spec: core.PodSpec{NodeName: node}, Status: core.PodStatus{Phase: core.PodRunning, Conditions: []core.PodCondition{{Type: core.PodReady, Status: core.ConditionTrue}}}})
			slice.Endpoints = append(slice.Endpoints, discovery.Endpoint{NodeName: &node, TargetRef: &core.ObjectReference{Kind: "Pod", Name: podname, Namespace: ns}, Conditions: discovery.EndpointConditions{Ready: &ready}})
		}
		s.Slices = append(s.Slices, slice)
	}
	add("public-services", "nextcloud", "cloud.example.com", 0, false, []string{"node2", "node3"})
	alias := s.Ingresses[0].DeepCopy()
	alias.Name = "nextcloud-alias"
	alias.Spec.Rules[0].Host = "files.example.com"
	s.Ingresses = append(s.Ingresses, *alias)
	add("public-services", "immich", "photos.example.com", 0, false, []string{"node3"})
	add("infra", "rancher", "rancher.example.com", 0, false, []string{"node2"})
	add("infra", "grafana", "grafana.example.com", 0, false, []string{"node3"})
	add("myapps", "silverbullet", "notes.example.com", 0, false, nil)
	add("public-services", "uptime-kuma", "", 30001, false, []string{"node2"})
	add("infra", "local-console", "", 30002, true, []string{"node2", "node3", "node5"})
	add("infra", "redis", "", 30379, false, []string{"node5"})
	s.Services[len(s.Services)-1].Spec.Ports[0].Name = "redis"
	return &DemoSource{s}
}
