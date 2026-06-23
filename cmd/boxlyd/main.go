// Command boxlyd is the Boxly control-plane microservice: it exposes the VM API
// and provisions Kubernetes workloads, keeps the warm pool full, and reaps
// expired sandboxes.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/SWITCHin2/boxly/internal/config"
	"github.com/SWITCHin2/boxly/internal/demand"
	"github.com/SWITCHin2/boxly/internal/janitor"
	"github.com/SWITCHin2/boxly/internal/k8s"
	"github.com/SWITCHin2/boxly/internal/pool"
	"github.com/SWITCHin2/boxly/internal/server"
	"github.com/SWITCHin2/boxly/internal/settings"
	"github.com/SWITCHin2/boxly/internal/template"
	"github.com/SWITCHin2/boxly/internal/vm"
)

func main() {
	cfg := config.LoadServer()

	client, err := k8s.New(cfg.Kubeconfig)
	if err != nil {
		log.Fatalf("kubernetes client: %v", err)
	}

	mgr := vm.NewManager(client, cfg.Namespace, cfg.DefaultImage, cfg.DefaultTTL, cfg.PullSecrets)
	tracker := demand.New(cfg.PoolDecay)

	// Seed runtime settings from env, then prefer any persisted ConfigMap.
	seed := settings.Settings{
		PoolMin: cfg.PoolMin, PoolMax: cfg.PoolMax, PoolDecay: cfg.PoolDecay,
		DefaultTTLSeconds: int(cfg.DefaultTTL.Seconds()), DefaultImage: cfg.DefaultImage,
		PullSecrets:     cfg.PullSecrets,
		DefaultUserDays: cfg.DefaultUserDays,
		MaxUserDays:     cfg.MaxUserDays,
	}
	if loaded, ok, err := settings.Load(context.Background(), client.Clientset, cfg.Namespace); err != nil {
		log.Printf("settings: load: %v (using env defaults)", err)
	} else if ok {
		seed = loaded
		log.Printf("settings: loaded from %s ConfigMap", settings.ConfigMapName)
	}
	apply := func(s settings.Settings) {
		template.LoadCustom(s.Templates)
		names := append([]string(nil), s.PullSecrets...)
		if name, err := settings.ApplyPullSecret(context.Background(), client.Clientset, cfg.Namespace, s.PullSecretYAML); err != nil {
			log.Printf("settings: apply pull secret: %v", err)
		} else if name != "" {
			names = append(names, name)
		}
		mgr.SetPullSecrets(names)
		mgr.SetDefaults(s.DefaultImage, time.Duration(s.DefaultTTLSeconds)*time.Second)
		tracker.SetDecay(s.PoolDecay)
	}
	store := settings.NewStore(seed, settings.Saver(client.Clientset, cfg.Namespace), apply)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go pool.New(mgr, tracker, store).Run(ctx)
	go janitor.New(mgr).Run(ctx)

	srv := &http.Server{
		Addr:    cfg.Addr,
		Handler: server.New(mgr, client, cfg.Token, cfg.AdminToken, tracker, store).Handler(),
	}

	go func() {
		log.Printf("boxlyd listening on %s (namespace=%s, pool=%d..%d)", cfg.Addr, cfg.Namespace, cfg.PoolMin, cfg.PoolMax)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("serve: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}
