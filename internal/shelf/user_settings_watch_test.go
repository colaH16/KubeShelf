package shelf

import (
	"context"
	"testing"
	"time"

	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientfeatures "k8s.io/client-go/features"
	featuretesting "k8s.io/client-go/features/testing"
	"k8s.io/client-go/kubernetes/fake"
)

func TestUserConfigMapWatchRefreshesExternalChanges(t *testing.T) {
	t.Setenv("POD_NAMESPACE", "public-services")
	// The fake API supports traditional list/watch, not streaming initial lists.
	featuretesting.SetFeatureDuringTest(t, clientfeatures.WatchListClient, false)
	client := fake.NewClientset(&core.ConfigMap{ObjectMeta: metav1.ObjectMeta{
		Name: "kubeshelf-user-test", Namespace: "public-services", ResourceVersion: "1",
		Labels: map[string]string{"kubeshelf.io/user-settings": "true"},
	}})
	source := &KubernetesSource{client: client}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := make(chan struct{}, 8)
	done := make(chan struct{})
	go func() {
		source.RunUserSettings(ctx, func(context.Context) error { calls <- struct{}{}; return nil })
		close(done)
	}()
	wait := func() {
		t.Helper()
		select {
		case <-calls:
		case <-time.After(5 * time.Second):
			t.Fatal("ConfigMap event did not refresh Git snapshot")
		}
	}
	wait()
	cm, _ := client.CoreV1().ConfigMaps("public-services").Get(ctx, "kubeshelf-user-test", metav1.GetOptions{})
	cm.ResourceVersion = "2"
	cm.Data = map[string]string{"favorites.json": "changed"}
	if _, err := client.CoreV1().ConfigMaps("public-services").Update(ctx, cm, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	wait()
	if err := client.CoreV1().ConfigMaps("public-services").Delete(ctx, cm.Name, metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	wait()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("watch did not stop")
	}
}
