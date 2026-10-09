package shelf

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"
)

var ErrConflict = errors.New("설정이 다른 곳에서 변경됐습니다. 최신 설정을 다시 불러와 주세요")

func randomID() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func decodeSettings(data []byte) (Settings, error) {
	var s Settings
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&s); err != nil {
		return s, fmt.Errorf("설정 파일 형식이 올바르지 않습니다")
	}
	if err := ValidateSettings(&s); err != nil {
		return s, err
	}
	return s, nil
}

type ApplyStatus struct {
	DesiredRevision string    `json:"desiredRevision"`
	AppliedRevision string    `json:"appliedRevision"`
	Commit          string    `json:"commit"`
	Pending         bool      `json:"pending"`
	Error           string    `json:"error,omitempty"`
	AppliedAt       time.Time `json:"appliedAt"`
}
type Store struct {
	profiles         map[string]FavoriteProfile // Demo only; protected by saveMu.
	mu               sync.RWMutex
	saveMu           sync.Mutex
	cfg              Config
	applied, desired Settings
	commit           string
	lastFile         []byte
	applyError       string
	appliedAt        time.Time
	repo             *gitRepository
}
type gitRepository struct {
	cfg        Config
	dir        string
	allowLocal bool
}

func OpenStore(ctx context.Context, cfg Config) (*Store, error) {
	s := &Store{cfg: cfg, applied: EmptySettings(), desired: EmptySettings()}
	if cfg.Demo {
		s.applied = demoSettings(cfg)
		s.desired = cloneSettings(s.applied)
		s.commit = "demo-initial"
		s.appliedAt = time.Now()
		return s, nil
	}
	if err := s.loadApplied(); err != nil {
		return nil, err
	}
	if !regexp.MustCompile(`^(ssh://[^\s]+|[A-Za-z0-9._-]+@[A-Za-z0-9._-]+:[^\s]+)$`).MatchString(cfg.GitURL) {
		return nil, fmt.Errorf("Git URL must use SSH")
	}
	if strings.ContainsAny(cfg.GitBranch, " \n\r~^:?*[\\") || strings.HasPrefix(cfg.GitBranch, "-") || strings.Contains(cfg.GitBranch, "..") {
		return nil, fmt.Errorf("invalid Git branch")
	}
	clean := filepath.Clean(cfg.GitPath)
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, ".git/") || clean == ".git" {
		return nil, fmt.Errorf("invalid Git manifest path")
	}
	repo := &gitRepository{cfg: cfg, dir: filepath.Join(cfg.WorkDir, "repository")}
	s.repo = repo
	if err := repo.init(ctx); err != nil {
		return nil, err
	}
	if err := s.Refresh(ctx); err != nil {
		return nil, err
	}
	return s, nil
}
func (s *Store) Applied() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneSettings(s.applied)
}
func (s *Store) Desired() (Settings, string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneSettings(s.desired), settingsVersion(s.desired)
}

// Scope conflict detection to the shared settings file, not unrelated user commits.
func settingsVersion(s Settings) string {
	data, _ := json.Marshal(s)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
func (s *Store) Status() ApplyStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return ApplyStatus{DesiredRevision: s.desired.Revision, AppliedRevision: s.applied.Revision, Commit: s.commit, Pending: s.desired.Revision != s.applied.Revision, Error: s.applyError, AppliedAt: s.appliedAt}
}
func (s *Store) loadApplied() error {
	data, err := os.ReadFile(s.cfg.SettingsFile)
	if err != nil {
		return fmt.Errorf("mounted runtime settings unavailable")
	}
	s.mu.RLock()
	same := bytes.Equal(data, s.lastFile)
	s.mu.RUnlock()
	if same {
		s.mu.Lock()
		s.applyError = ""
		s.mu.Unlock()
		return nil
	}
	next, err := decodeSettings(data)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.applied = next
	s.lastFile = append([]byte{}, data...)
	s.appliedAt = time.Now().UTC()
	s.applyError = ""
	s.mu.Unlock()
	return nil
}
func (s *Store) Run(ctx context.Context) {
	if s.cfg.Demo {
		return
	}
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			before := s.Applied().Revision
			if err := s.loadApplied(); err != nil {
				s.mu.Lock()
				s.applyError = err.Error()
				s.mu.Unlock()
				continue
			}
			// A projected ConfigMap change, rather than a periodic Git poll, triggers refresh.
			if s.Applied().Revision != before {
				refreshCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
				_ = s.Refresh(refreshCtx)
				cancel()
			}
		}
	}
}
func (s *Store) Refresh(ctx context.Context) error {
	if s.cfg.Demo {
		return nil
	}
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	next, commit, err := s.repo.read(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.desired = next
	s.commit = commit
	s.mu.Unlock()
	return nil
}
func (s *Store) Save(ctx context.Context, base string, next Settings, actor string) (ApplyStatus, error) {
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	if err := ValidateSettings(&next); err != nil {
		return s.Status(), err
	}
	old, commit := s.Desired()
	if !s.cfg.Demo {
		var err error
		old, commit, err = s.repo.read(ctx)
		if err != nil {
			return s.Status(), err
		}
		s.mu.Lock()
		s.desired = old
		s.commit = commit
		s.mu.Unlock()
	}
	if base != settingsVersion(old) {
		return s.Status(), ErrConflict
	}
	next.Revision = randomID()
	msg := changeSummary(old, next) + "\n\nEdited by: " + strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return ' '
		}
		return r
	}, actor)
	newCommit := "demo-" + next.Revision
	if !s.cfg.Demo {
		var err error
		newCommit, err = s.repo.write(ctx, next, msg)
		if err != nil {
			return s.Status(), err
		}
	}
	s.mu.Lock()
	s.desired = cloneSettings(next)
	s.commit = newCommit
	s.mu.Unlock()
	if s.cfg.Demo {
		go func(v Settings) {
			time.Sleep(1500 * time.Millisecond)
			s.mu.Lock()
			defer s.mu.Unlock()
			if s.desired.Revision == v.Revision {
				s.applied = v
				s.appliedAt = time.Now()
			}
		}(cloneSettings(next))
	}
	// Production never changes the applied settings here. Fleet owns that transition.
	return s.Status(), nil
}
func shellQuote(v string) string { return "'" + strings.ReplaceAll(v, "'", "'\"'\"'") + "'" }
func (g *gitRepository) run(ctx context.Context, input []byte, args ...string) ([]byte, error) {
	protocol := "never"
	if g.allowLocal {
		protocol = "always"
	}
	base := []string{"-c", "core.hooksPath=/dev/null", "-c", "protocol.file.allow=" + protocol, "-c", "commit.gpgsign=false", "-c", "user.name=KubeShelf", "-c", "user.email=kubeshelf@users.noreply.local"}
	if !g.allowLocal {
		// Secret volumes can be group-readable. OpenSSH requires a private copy with mode 0600.
		data, err := os.ReadFile(g.cfg.SSHKeyFile)
		if err != nil {
			return nil, fmt.Errorf("Git SSH 키를 읽을 수 없습니다")
		}
		private := filepath.Join(g.cfg.WorkDir, "git-identity")
		if err := os.WriteFile(private, data, 0600); err != nil {
			return nil, err
		}
		if err := os.Chmod(private, 0600); err != nil {
			return nil, err
		}
		ssh := "ssh -F /dev/null -o BatchMode=yes -o IdentitiesOnly=yes -o StrictHostKeyChecking=yes -o ConnectTimeout=10 -o UserKnownHostsFile=" + shellQuote(g.cfg.KnownHostsFile) + " -i " + shellQuote(private)
		base = append(base, "-c", "core.sshCommand="+ssh)
	}
	cmd := exec.CommandContext(ctx, "git", append(base, args...)...)
	cmd.Dir = g.dir
	cmd.Stdin = bytes.NewReader(input)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("Git %s 작업에 실패했습니다", args[0])
	}
	return out, nil
}
func (g *gitRepository) init(ctx context.Context) error {
	if err := os.MkdirAll(g.dir, 0700); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(g.dir, ".git")); os.IsNotExist(err) {
		if _, err = g.run(ctx, nil, "init", "--initial-branch="+g.cfg.GitBranch); err != nil {
			return err
		}
		if _, err = g.run(ctx, nil, "remote", "add", "origin", g.cfg.GitURL); err != nil {
			return err
		}
	}
	_, err := g.run(ctx, nil, "remote", "set-url", "origin", g.cfg.GitURL)
	return err
}
func (g *gitRepository) read(ctx context.Context) (Settings, string, error) {
	if _, err := g.run(ctx, nil, "fetch", "--depth=20", "origin", g.cfg.GitBranch); err != nil {
		return Settings{}, "", err
	}
	if _, err := g.run(ctx, nil, "reset", "--hard", "FETCH_HEAD"); err != nil {
		return Settings{}, "", err
	}
	head, err := g.run(ctx, nil, "rev-parse", "HEAD")
	if err != nil {
		return Settings{}, "", err
	}
	// git show reads a blob without following a symlink from the repository.
	data, err := g.run(ctx, nil, "show", "HEAD:"+g.cfg.GitPath)
	if err != nil {
		return Settings{}, "", err
	}
	var cm core.ConfigMap
	if err = yaml.UnmarshalStrict(data, &cm); err != nil {
		return Settings{}, "", fmt.Errorf("Git ConfigMap 형식 오류")
	}
	if cm.Kind != "ConfigMap" || cm.APIVersion != "v1" || cm.Name != env("KUBESHELF_CONFIGMAP_NAME", "kubeshelf-settings") || cm.Namespace != env("POD_NAMESPACE", "public-services") {
		return Settings{}, "", fmt.Errorf("Git ConfigMap 대상이 일치하지 않습니다")
	}
	s, err := decodeSettings([]byte(cm.Data["settings.json"]))
	return s, strings.TrimSpace(string(head)), err
}
func encodeSettings(s Settings) ([]byte, error) {
	data, _ := json.MarshalIndent(s, "", "  ")
	cm := core.ConfigMap{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"}, ObjectMeta: metav1.ObjectMeta{Name: env("KUBESHELF_CONFIGMAP_NAME", "kubeshelf-settings"), Namespace: env("POD_NAMESPACE", "public-services")}, Data: map[string]string{"settings.json": string(data) + "\n"}}
	return yaml.Marshal(cm)
}
func (g *gitRepository) write(ctx context.Context, s Settings, msg string) (string, error) {
	encoded, err := encodeSettings(s)
	if err != nil {
		return "", err
	}
	return g.writeFiles(ctx, map[string][]byte{g.cfg.GitPath: encoded}, msg)
}
func (g *gitRepository) writeFiles(ctx context.Context, files map[string][]byte, msg string) (string, error) {
	// The callers supply either the configured settings path or a server-derived
	// user ConfigMap path. No path from an HTTP request reaches the Git index.
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		blob, err := g.run(ctx, files[path], "hash-object", "-w", "--stdin")
		if err != nil {
			return "", err
		}
		if _, err = g.run(ctx, nil, "update-index", "--add", "--cacheinfo", "100644,"+strings.TrimSpace(string(blob))+","+path); err != nil {
			return "", err
		}
	}
	var err error
	if _, err = g.run(ctx, []byte(msg), "commit", "--file=-"); err != nil {
		return "", err
	}
	head, err := g.run(ctx, nil, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	if _, err = g.run(ctx, nil, "push", "origin", "HEAD:refs/heads/"+g.cfg.GitBranch); err != nil {
		// A lost acknowledgement can follow a successful push. Confirm before reporting failure.
		remote, e := g.run(ctx, nil, "ls-remote", "origin", "refs/heads/"+g.cfg.GitBranch)
		if e != nil || !strings.HasPrefix(string(remote), strings.TrimSpace(string(head))+"\t") {
			return "", fmt.Errorf("Git push에 실패했습니다. 원격 변경 또는 연결 상태를 확인해 주세요")
		}
	}
	return strings.TrimSpace(string(head)), nil
}
func demoSettings(c Config) Settings {
	s := EmptySettings()
	s.Namespaces["public-services"] = Policy{Mode: "public"}
	s.Namespaces["infra"] = Policy{Mode: "admin"}
	s.Namespaces["myapps"] = Policy{Mode: "restricted", Groups: []string{"family"}}
	s.NodePortNamespaces["public-services"] = Policy{Mode: "admin"}
	s.NodePortNamespaces["infra"] = Policy{Mode: "admin"}
	s.Manual = []ManualApp{{ID: "manual-proxmox", Name: "Proxmox", URLs: []ManualURL{{ID: "manual-proxmox-main", Label: "Management", URL: "https://proxmox.example.com"}}}, {ID: "manual-ldap", Name: "Directory Admin", URLs: []ManualURL{{ID: "manual-ldap-main", Label: "LDAP", URL: "https://directory.example.com"}}}}
	s.Apps["manual-proxmox"] = AppSettings{Icon: "server"}
	s.Apps["manual-ldap"] = AppSettings{Icon: "users"}
	s.Targets = []Target{{ID: "domain-edge", Name: "edge.example.com", Host: "edge.example.com", NodeName: "node3", Manual: true, Visibility: &Policy{Mode: "public"}}}
	cat := BuildCatalog(NewDemoSource().Snapshot(), s, c, Identity{Admin: true})
	for _, card := range cat.Cards {
		a := s.Apps[card.ID]
		a.Name = map[string]string{"nextcloud": "Nextcloud", "immich": "Immich", "grafana": "Grafana", "rancher": "Rancher", "silverbullet": "SilverBullet", "uptime-kuma": "Uptime Kuma", "local-console": "Local Console", "redis": "Redis"}[card.Name]
		a.Reviewed = map[string]string{}
		for _, e := range card.Endpoints {
			if card.Name != "redis" && card.Name != "silverbullet" {
				a.Reviewed[e.ID] = e.Fingerprint
			}
		}
		s.Apps[card.ID] = a
	}
	return s
}
