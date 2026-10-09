package shelf

import (
	"context"
	"log"
	"time"

	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"
)

// Fleet can update user files without changing the mounted shared ConfigMap.
// Kubernetes events refresh the confirmed Git snapshot; there is no Git poll.
func (s *KubernetesSource) RunUserSettings(ctx context.Context, refresh func(context.Context) error) {
	changes := make(chan struct{}, 1)
	notify := func() {
		select {
		case changes <- struct{}{}:
		default:
		}
	}
	selector := "kubeshelf.io/user-settings=true"
	configmaps := s.client.CoreV1().ConfigMaps(env("POD_NAMESPACE", "public-services"))
	informer := cache.NewSharedIndexInformer(&cache.ListWatch{
		ListWithContextFunc: func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
			options.LabelSelector = selector
			return configmaps.List(ctx, options)
		},
		WatchFuncWithContext: func(ctx context.Context, options metav1.ListOptions) (watch.Interface, error) {
			options.LabelSelector = selector
			return configmaps.Watch(ctx, options)
		},
	}, &core.ConfigMap{}, 0, cache.Indexers{})
	_, _ = informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(any) { notify() },
		UpdateFunc: func(before, after any) {
			if before.(*core.ConfigMap).ResourceVersion != after.(*core.ConfigMap).ResourceVersion {
				notify()
			}
		},
		DeleteFunc: func(any) { notify() },
	})
	go informer.RunWithContext(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-changes:
		}
		// Coalesce a Fleet batch of account ConfigMaps into one fetch.
		delay := time.Second
		for {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			select {
			case <-changes:
			default:
			}
			refreshCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			err := refresh(refreshCtx)
			cancel()
			if err == nil {
				break
			}
			log.Printf("user settings refresh delayed: %v", err)
			delay = 5 * time.Second
		}
	}
}
