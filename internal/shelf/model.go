package shelf

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	core "k8s.io/api/core/v1"
	discovery "k8s.io/api/discovery/v1"
	networking "k8s.io/api/networking/v1"
)

type Config struct {
	Listen, PublicURL, Cluster, Kubeconfig, SettingsFile, WorkDir              string
	GitURL, GitBranch, GitPath, SSHKeyFile, KnownHostsFile                     string
	OIDCIssuer, OIDCClientID, OIDCSecretFile, DirectoryURL, DirectoryTokenFile string
	AdminSubjects                                                              []string
	IngressSchemes                                                             map[string]string
	Demo                                                                       bool
}

func ConfigFromEnv() (Config, error) {
	c := Config{Listen: env("KUBESHELF_LISTEN", ":8080"), PublicURL: os.Getenv("KUBESHELF_PUBLIC_URL"), Cluster: env("KUBESHELF_CLUSTER_NAME", "cluster"), Kubeconfig: os.Getenv("KUBESHELF_KUBECONFIG"), SettingsFile: env("KUBESHELF_SETTINGS_FILE", "/config/runtime/settings.json"), WorkDir: env("KUBESHELF_WORK_DIR", "/work"), GitURL: os.Getenv("KUBESHELF_GIT_URL"), GitBranch: env("KUBESHELF_GIT_BRANCH", "main"), GitPath: env("KUBESHELF_GIT_PATH", "runtime/settings.yaml"), SSHKeyFile: env("KUBESHELF_SSH_KEY_FILE", "/credentials/git/ssh-privatekey"), KnownHostsFile: env("KUBESHELF_KNOWN_HOSTS_FILE", "/credentials/git-host/known_hosts"), OIDCIssuer: os.Getenv("KUBESHELF_OIDC_ISSUER"), OIDCClientID: os.Getenv("KUBESHELF_OIDC_CLIENT_ID"), OIDCSecretFile: env("KUBESHELF_OIDC_SECRET_FILE", "/credentials/auth/client-secret"), DirectoryURL: os.Getenv("KUBESHELF_DIRECTORY_URL"), DirectoryTokenFile: env("KUBESHELF_DIRECTORY_TOKEN_FILE", "/credentials/auth/directory-token"), Demo: os.Getenv("KUBESHELF_DEMO") == "true", IngressSchemes: map[string]string{}}
	for _, v := range strings.Split(os.Getenv("KUBESHELF_ADMIN_SUBJECTS"), ",") {
		if v = strings.TrimSpace(v); v != "" {
			c.AdminSubjects = append(c.AdminSubjects, v)
		}
	}
	if c.PublicURL == "" && c.Demo {
		c.PublicURL = "http://localhost:8080"
	}
	if err := validateURL(c.PublicURL); err != nil {
		return c, fmt.Errorf("KUBESHELF_PUBLIC_URL is required and must be an HTTP(S) origin")
	}
	u, _ := url.Parse(c.PublicURL)
	if u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.Fragment != "" {
		return c, fmt.Errorf("public URL must be an origin")
	}
	c.PublicURL = strings.TrimRight(c.PublicURL, "/")
	if !c.Demo && (u.Scheme != "https" || c.OIDCIssuer == "" || c.OIDCClientID == "" || len(c.AdminSubjects) == 0 || c.GitURL == "") {
		return c, fmt.Errorf("production requires HTTPS, OIDC, admin subjects, and Git URL")
	}
	if v := os.Getenv("KUBESHELF_INGRESS_SCHEMES"); v != "" {
		if err := json.Unmarshal([]byte(v), &c.IngressSchemes); err != nil {
			return c, err
		}
	}
	for _, v := range c.IngressSchemes {
		if v != "https" && v != "http" {
			return c, fmt.Errorf("invalid ingress scheme")
		}
	}
	return c, nil
}
func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func stableID(p ...string) string {
	h := sha256.Sum256([]byte(strings.Join(p, "\x00")))
	return hex.EncodeToString(h[:12])
}
func sortedUnique(in []string) []string {
	m := map[string]bool{}
	out := []string{}
	for _, s := range in {
		if !m[s] {
			m[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}
func contains(in []string, s string) bool {
	for _, x := range in {
		if x == s {
			return true
		}
	}
	return false
}

type Policy struct {
	Mode   string   `json:"mode"`
	Groups []string `json:"groups,omitempty"`
	Users  []string `json:"users,omitempty"`
}
type Identity struct {
	Subject  string   `json:"subject"`
	Name     string   `json:"name"`
	Username string   `json:"username"`
	Groups   []string `json:"groups"`
	Admin    bool     `json:"admin"`
}

func (p Policy) Allows(u Identity) bool {
	if u.Admin {
		return true
	}
	if p.Mode == "public" {
		return true
	}
	if p.Mode != "restricted" || u.Subject == "" {
		return false
	}
	if contains(p.Users, u.Subject) {
		return true
	}
	for _, g := range u.Groups {
		if contains(p.Groups, g) {
			return true
		}
	}
	return false
}

type EndpointSettings struct {
	Label      string  `json:"label,omitempty"`
	URL        string  `json:"url,omitempty"`
	Scheme     string  `json:"scheme,omitempty"`
	Visibility *Policy `json:"visibility,omitempty"`
}
type AppSettings struct {
	Name        string                      `json:"name,omitempty"`
	Icon        string                      `json:"icon,omitempty"`
	Description string                      `json:"description,omitempty"`
	Hidden      bool                        `json:"hidden"`
	Visibility  *Policy                     `json:"visibility,omitempty"`
	Reviewed    map[string]string           `json:"reviewed,omitempty"`
	Endpoints   map[string]EndpointSettings `json:"endpoints,omitempty"`
}
type ManualApp struct {
	ID   string      `json:"id"`
	Name string      `json:"name"`
	URLs []ManualURL `json:"urls"`
}
type ManualURL struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	URL   string `json:"url"`
}
type Target struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Host       string  `json:"host"`
	NodeName   string  `json:"nodeName,omitempty"`
	Manual     bool    `json:"manual"`
	Visibility *Policy `json:"visibility,omitempty"`
	TCPEnabled bool    `json:"tcpEnabled"`
}
type Settings struct {
	SchemaVersion      int                    `json:"schemaVersion"`
	Revision           string                 `json:"revision"`
	Namespaces         map[string]Policy      `json:"namespaces"`
	NodePortNamespaces map[string]Policy      `json:"nodePortNamespaces"`
	Apps               map[string]AppSettings `json:"apps"`
	Manual             []ManualApp            `json:"manual"`
	Targets            []Target               `json:"targets"`
	Assignments        map[string]string      `json:"assignments"`
}

func EmptySettings() Settings {
	return Settings{SchemaVersion: 1, Revision: "initial", Namespaces: map[string]Policy{}, NodePortNamespaces: map[string]Policy{}, Apps: map[string]AppSettings{}, Manual: []ManualApp{}, Targets: []Target{}, Assignments: map[string]string{}}
}
func cloneSettings(s Settings) Settings {
	b, _ := json.Marshal(s)
	var c Settings
	_ = json.Unmarshal(b, &c)
	return c
}
func validatePolicy(p *Policy) error {
	if p == nil {
		return nil
	}
	if p.Mode != "public" && p.Mode != "restricted" && p.Mode != "admin" {
		return fmt.Errorf("invalid visibility policy")
	}
	if len(p.Users)+len(p.Groups) > 1000 {
		return fmt.Errorf("too many permission entries")
	}
	for _, x := range append(append([]string{}, p.Users...), p.Groups...) {
		if x == "" || len(x) > 256 {
			return fmt.Errorf("invalid permission identity")
		}
	}
	return nil
}
func ValidateSettings(s *Settings) error {
	if s.SchemaVersion != 1 || s.Revision == "" || len(s.Revision) > 100 {
		return fmt.Errorf("invalid settings version")
	}
	if s.Namespaces == nil {
		s.Namespaces = map[string]Policy{}
	}
	if s.NodePortNamespaces == nil {
		s.NodePortNamespaces = map[string]Policy{}
	}
	if s.Apps == nil {
		s.Apps = map[string]AppSettings{}
	}
	if s.Manual == nil {
		s.Manual = []ManualApp{}
	}
	if s.Targets == nil {
		s.Targets = []Target{}
	}
	if s.Assignments == nil {
		s.Assignments = map[string]string{}
	}
	if len(s.Apps) > 10000 || len(s.Manual) > 1000 || len(s.Targets) > 100 || len(s.Assignments) > 10000 {
		return fmt.Errorf("too many settings entries")
	}
	for ns, p := range s.Namespaces {
		if ns == "" || len(ns) > 253 {
			return fmt.Errorf("invalid namespace")
		}
		if err := validatePolicy(&p); err != nil {
			return err
		}
	}
	for ns, p := range s.NodePortNamespaces {
		if ns == "" || len(ns) > 253 {
			return fmt.Errorf("invalid NodePort namespace")
		}
		if err := validatePolicy(&p); err != nil {
			return err
		}
	}
	for id, a := range s.Apps {
		if id == "" || len(id) > 100 || len(a.Name) > 200 || len(a.Description) > 2000 || len(a.Icon) > 2048 {
			return fmt.Errorf("invalid card settings")
		}
		if err := validatePolicy(a.Visibility); err != nil {
			return err
		}
		if strings.Contains(a.Icon, "://") {
			if err := validateURL(a.Icon); err != nil {
				return err
			}
		}
		for eid, e := range a.Endpoints {
			if eid == "" || len(eid) > 100 || len(e.Label) > 200 {
				return fmt.Errorf("invalid endpoint settings")
			}
			if e.URL != "" {
				if err := validateURL(e.URL); err != nil {
					return err
				}
			}
			if e.Scheme != "" && e.Scheme != "http" && e.Scheme != "https" && e.Scheme != "tcp" {
				return fmt.Errorf("invalid protocol")
			}
			if err := validatePolicy(e.Visibility); err != nil {
				return err
			}
		}
	}
	ids := map[string]bool{}
	for _, m := range s.Manual {
		if !strings.HasPrefix(m.ID, "manual-") || ids[m.ID] || m.Name == "" || len(m.Name) > 200 || len(m.URLs) == 0 || len(m.URLs) > 50 {
			return fmt.Errorf("invalid manual service")
		}
		ids[m.ID] = true
		seen := map[string]bool{}
		for _, u := range m.URLs {
			if u.ID == "" || seen[u.ID] || len(u.ID) > 100 || len(u.Label) > 200 {
				return fmt.Errorf("invalid manual address")
			}
			seen[u.ID] = true
			if err := validateURL(u.URL); err != nil {
				return err
			}
		}
	}
	ids = map[string]bool{}
	for _, t := range s.Targets {
		if !strings.HasPrefix(t.ID, "domain-") || ids[t.ID] || t.Name == "" || len(t.Name) > 200 {
			return fmt.Errorf("invalid node domain")
		}
		ids[t.ID] = true
		if err := validateHost(t.Host); err != nil {
			return err
		}
		if err := validatePolicy(t.Visibility); err != nil {
			return err
		}
	}
	for e, g := range s.Assignments {
		if e == "" || len(e) > 100 || g == "" || len(g) > 100 {
			return fmt.Errorf("invalid card grouping")
		}
	}
	b, _ := json.Marshal(s)
	if len(b) > 650000 {
		return fmt.Errorf("settings exceed ConfigMap size budget")
	}
	return nil
}
func validateURL(raw string) error {
	u, e := url.Parse(raw)
	if e != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || strings.ContainsAny(raw, "\r\n\t\\") {
		return fmt.Errorf("valid HTTP or HTTPS address required")
	}
	return nil
}
func validateHost(h string) error {
	if net.ParseIP(h) != nil {
		return nil
	}
	if h == "" || len(h) > 253 || strings.ContainsAny(h, "/:@?#\\ \r\n\t") {
		return fmt.Errorf("hostname without scheme or port required")
	}
	for _, s := range strings.Split(h, ".") {
		if s == "" || len(s) > 63 || s[0] == '-' || s[len(s)-1] == '-' {
			return fmt.Errorf("invalid hostname")
		}
		for _, c := range s {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return fmt.Errorf("invalid hostname")
			}
		}
	}
	return nil
}

type Snapshot struct {
	Namespaces []core.Namespace
	Services   []core.Service
	Pods       []core.Pod
	Nodes      []core.Node
	Slices     []discovery.EndpointSlice
	Ingresses  []networking.Ingress
	Connected  bool
	UpdatedAt  time.Time
	Problem    string
}
type Source interface{ Snapshot() Snapshot }
type Health struct {
	State      string   `json:"state"`
	Ready      int      `json:"ready"`
	Total      int      `json:"total"`
	Reason     string   `json:"reason"`
	ReadyNodes []string `json:"readyNodes,omitempty"`
}
type Connection struct {
	TargetID string `json:"targetId"`
	Name     string `json:"name"`
	URL      string `json:"url"`
	Address  string `json:"address"`
	NodeName string `json:"nodeName,omitempty"`
}
type Endpoint struct {
	ID          string       `json:"id"`
	AppID       string       `json:"appId"`
	Label       string       `json:"label"`
	URL         string       `json:"url"`
	Kind        string       `json:"kind"`
	Namespace   string       `json:"namespace,omitempty"`
	Ingresses   []string     `json:"ingresses,omitempty"`
	Service     string       `json:"service,omitempty"`
	Port        string       `json:"port,omitempty"`
	NodePort    int32        `json:"nodePort,omitempty"`
	Scheme      string       `json:"scheme"`
	Protocol    string       `json:"protocol,omitempty"`
	Local       bool         `json:"local"`
	NeedsURL    bool         `json:"needsURL"`
	Health      Health       `json:"health"`
	Targets     []Connection `json:"targets"`
	Fingerprint string       `json:"fingerprint,omitempty"`
}
type Card struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Icon        string     `json:"icon"`
	Description string     `json:"description"`
	Source      string     `json:"source"`
	Namespace   string     `json:"namespace,omitempty"`
	Hidden      bool       `json:"hidden"`
	New         bool       `json:"new"`
	Changed     bool       `json:"changed"`
	Endpoints   []Endpoint `json:"endpoints"`
}
type NamespaceInfo struct {
	Name               string `json:"name"`
	Configured         bool   `json:"configured"`
	Services           int    `json:"services"`
	Policy             Policy `json:"policy"`
	IngressConfigured  bool   `json:"ingressConfigured"`
	IngressServices    int    `json:"ingressServices"`
	NodePortConfigured bool   `json:"nodePortConfigured"`
	NodePortServices   int    `json:"nodePortServices"`
	NodePortPolicy     Policy `json:"nodePortPolicy"`
}
type Catalog struct {
	Cards           []Card          `json:"cards"`
	Targets         []Target        `json:"targets"`
	Namespaces      []NamespaceInfo `json:"namespaces,omitempty"`
	Connected       bool            `json:"connected"`
	UpdatedAt       time.Time       `json:"updatedAt"`
	Problem         string          `json:"problem,omitempty"`
	Identity        Identity        `json:"identity"`
	CSRF            string          `json:"csrf,omitempty"`
	Demo            bool            `json:"demo"`
	Version         string          `json:"version"`
	AppliedRevision string          `json:"appliedRevision"`
}
