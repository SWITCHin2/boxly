// Package pool keeps a predictive set of warm boxes ready per template, so
// creation is an instant label-patch claim instead of a cold start. Each
// template's warm count tracks an EWMA of its create demand (see internal/demand),
// and warm boxes are pre-prepared (setup script applied) before being claimable.
package pool

import (
	"context"
	"log"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"

	"github.com/SWITCHin2/boxly/internal/demand"
	"github.com/SWITCHin2/boxly/internal/settings"
	"github.com/SWITCHin2/boxly/internal/template"
	"github.com/SWITCHin2/boxly/internal/vm"
)

// Reconciler maintains per-template warm pools sized from live demand. Pool
// bounds are read live from the settings store so admin edits take effect.
type Reconciler struct {
	mgr      *vm.Manager
	tracker  *demand.Tracker
	store    *settings.Store
	interval time.Duration

	preparing sync.Map // podName -> struct{}, guards in-flight setup
}

func New(mgr *vm.Manager, tracker *demand.Tracker, store *settings.Store) *Reconciler {
	return &Reconciler{mgr: mgr, tracker: tracker, store: store, interval: 5 * time.Second}
}

// Run blocks until ctx is cancelled, reconciling immediately then on each tick.
func (r *Reconciler) Run(ctx context.Context) {
	r.reconcile(ctx)
	t := time.NewTicker(r.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.reconcile(ctx)
		}
	}
}

func (r *Reconciler) reconcile(ctx context.Context) {
	s := r.store.Get()
	r.tracker.SetDecay(s.PoolDecay)
	r.tracker.Tick()
	for _, tmpl := range template.All() {
		r.reconcileTemplate(ctx, tmpl, s.PoolMin, s.PoolMax)
	}
}

func (r *Reconciler) reconcileTemplate(ctx context.Context, tmpl template.Template, min, max int) {
	desired := r.tracker.Desired(tmpl.ID, effMin(tmpl, min), effMax(tmpl, max))

	warm, err := r.mgr.ListWarm(ctx, tmpl.ID)
	if err != nil {
		log.Printf("pool[%s]: list warm: %v", tmpl.ID, err)
		return
	}

	// Prepare any running-but-unready warm boxes (async; setup can take seconds).
	live := warm[:0]
	for i := range warm {
		p := &warm[i]
		if p.DeletionTimestamp != nil {
			continue
		}
		live = append(live, *p)
		if p.Labels[vm.LabelReady] != "true" && p.Status.Phase == corev1.PodRunning {
			r.prepareAsync(p.Name, tmpl)
		}
	}

	switch {
	case len(live) < desired:
		for i := 0; i < desired-len(live); i++ {
			if err := r.mgr.CreateWarmPod(ctx, tmpl); err != nil {
				log.Printf("pool[%s]: create warm: %v", tmpl.ID, err)
				break
			}
		}
		if desired > len(live) {
			log.Printf("pool[%s]: warming %d→%d (demand %.2f)", tmpl.ID, len(live), desired, r.tracker.Rate(tmpl.ID))
		}
	case len(live) > desired:
		r.deleteSurplus(ctx, tmpl.ID, live, len(live)-desired)
	}
}

// deleteSurplus removes n warm boxes, preferring not-yet-ready ones.
func (r *Reconciler) deleteSurplus(ctx context.Context, templateID string, warm []corev1.Pod, n int) {
	ordered := append([]corev1.Pod(nil), warm...)
	// not-ready first, then ready
	notReady, ready := []corev1.Pod{}, []corev1.Pod{}
	for _, p := range ordered {
		if p.Labels[vm.LabelReady] == "true" {
			ready = append(ready, p)
		} else {
			notReady = append(notReady, p)
		}
	}
	victims := append(notReady, ready...)
	for i := 0; i < n && i < len(victims); i++ {
		if err := r.mgr.DeleteWarmPod(ctx, victims[i].Name); err != nil {
			log.Printf("pool[%s]: delete surplus: %v", templateID, err)
		}
	}
}

func (r *Reconciler) prepareAsync(podName string, tmpl template.Template) {
	if _, busy := r.preparing.LoadOrStore(podName, struct{}{}); busy {
		return
	}
	go func() {
		defer r.preparing.Delete(podName)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := r.mgr.PrepareWarm(ctx, podName, tmpl); err != nil {
			log.Printf("pool[%s]: prepare %s: %v", tmpl.ID, podName, err)
		}
	}()
}

func effMin(t template.Template, globalMin int) int {
	if t.WarmMin > globalMin {
		return t.WarmMin
	}
	return globalMin
}

func effMax(t template.Template, globalMax int) int {
	if t.WarmMax > 0 {
		return t.WarmMax
	}
	return globalMax
}
