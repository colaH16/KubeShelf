package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/colaH16/KubeShelf/internal/shelf"
	"github.com/colaH16/KubeShelf/internal/webassets"
)

var version = "dev"

func main() {
	cfg, err := shelf.ConfigFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	if cfg.Demo {
		cfg.IngressSchemes["demo-https"] = "https"
	}
	setupCtx, stop := context.WithTimeout(ctx, 60*time.Second)
	store, err := shelf.OpenStore(setupCtx, cfg)
	stop()
	if err != nil {
		log.Fatal(err)
	}
	var source shelf.Source
	if cfg.Demo {
		cfg.IngressSchemes["demo-https"] = "https"
		source = shelf.NewDemoSource()
	} else {
		source, err = shelf.NewKubernetesSource(ctx, cfg)
		if err != nil {
			log.Fatal(err)
		}
	}
	go store.Run(ctx)
	app := shelf.NewServer(cfg, store, source, version, webassets.Files())
	srv := &http.Server{Addr: cfg.Listen, Handler: app.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 16}
	go func() {
		<-ctx.Done()
		shutdown, end := context.WithTimeout(context.Background(), 10*time.Second)
		defer end()
		_ = srv.Shutdown(shutdown)
	}()
	log.Printf("KubeShelf %s listening on %s (demo=%t)", version, cfg.Listen, cfg.Demo)
	if err = srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Print(err)
		os.Exit(1)
	}
}
