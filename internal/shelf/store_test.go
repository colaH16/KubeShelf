package shelf

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"
)

func gitTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git test command failed: %s: %v", b, err)
	}
	return strings.TrimSpace(string(b))
}
func newTestStore(t *testing.T) (*Store, string, string) {
	t.Helper()
	t.Setenv("KUBESHELF_CONFIGMAP_NAME", "kubeshelf-settings")
	t.Setenv("POD_NAMESPACE", "public-services")
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	gitTest(t, root, "init", "--bare", "--initial-branch=main", remote)
	seed := filepath.Join(root, "seed")
	gitTest(t, root, "clone", remote, seed)
	_ = os.MkdirAll(filepath.Join(seed, "runtime"), 0700)
	initial := EmptySettings()
	data, _ := json.Marshal(initial)
	cm := core.ConfigMap{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"}, ObjectMeta: metav1.ObjectMeta{Name: "kubeshelf-settings", Namespace: "public-services"}, Data: map[string]string{"settings.json": string(data)}}
	manifest, _ := yaml.Marshal(cm)
	if err := os.WriteFile(filepath.Join(seed, "runtime/settings.yaml"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	gitTest(t, seed, "add", ".")
	gitTest(t, seed, "commit", "-m", "Initial settings")
	gitTest(t, seed, "push", "origin", "main")
	mounted := filepath.Join(root, "settings.json")
	_ = os.WriteFile(mounted, data, 0600)
	cfg := Config{SettingsFile: mounted, WorkDir: filepath.Join(root, "work"), GitURL: remote, GitBranch: "main", GitPath: "runtime/settings.yaml"}
	repo := &gitRepository{cfg: cfg, dir: filepath.Join(cfg.WorkDir, "repository"), allowLocal: true}
	ctx := context.Background()
	if err := repo.init(ctx); err != nil {
		t.Fatal(err)
	}
	s := &Store{cfg: cfg, repo: repo}
	if err := s.loadApplied(); err != nil {
		t.Fatal(err)
	}
	if err := s.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	return s, remote, seed
}
func TestPushDoesNotApplyUntilMountedRevisionArrives(t *testing.T) {
	s, remote, _ := newTestStore(t)
	before := s.Applied().Revision
	next, base := s.Desired()
	next.Namespaces["public-services"] = Policy{Mode: "public"}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	status, err := s.Save(ctx, base, next, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if !status.Pending || s.Applied().Revision != before {
		t.Fatal("push bypassed Fleet application boundary")
	}
	if !strings.Contains(gitTest(t, remote, "log", "-1", "--format=%B"), "namespace public-services visibility") {
		t.Fatal("commit omitted settings change")
	}
	desired, _ := s.Desired()
	data, _ := json.Marshal(desired)
	_ = os.WriteFile(s.cfg.SettingsFile, data, 0600)
	if err := s.loadApplied(); err != nil {
		t.Fatal(err)
	}
	if s.Status().Pending || s.Applied().Namespaces["public-services"].Mode != "public" {
		t.Fatal("mounted settings not applied")
	}
	if _, err = s.Save(ctx, base, next, "admin"); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale edit did not conflict: %v", err)
	}
}
func TestRejectedPushPreservesDesiredAndApplied(t *testing.T) {
	s, remote, _ := newTestStore(t)
	old, base := s.Desired()
	hook := filepath.Join(remote, "hooks/pre-receive")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	next := cloneSettings(old)
	next.Namespaces["private"] = Policy{Mode: "public"}
	_, err := s.Save(context.Background(), base, next, "admin")
	if err == nil {
		t.Fatal("rejected push reported success")
	}
	desired, current := s.Desired()
	if desired.Revision != old.Revision || current != base || s.Applied().Revision != old.Revision {
		t.Fatal("failed push modified live or desired settings")
	}
}
func TestMalformedMountedSettingsPreserveLastGoodState(t *testing.T) {
	s, _, _ := newTestStore(t)
	before := s.Applied()
	_ = os.WriteFile(s.cfg.SettingsFile, []byte(`{"schemaVersion":1,"revision":"bad","namespaces":{"x":{"mode":"typo"}}}`), 0600)
	if s.loadApplied() == nil {
		t.Fatal("invalid mounted config accepted")
	}
	if s.Applied().Revision != before.Revision {
		t.Fatal("invalid settings replaced previous state")
	}
}
