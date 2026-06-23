// Package janitor deletes expired sandbox VMs so disposable resources do not
// leak. Persistent VMs have no TTL and are ignored.
package janitor

import (
	"context"
	"log"
	"time"

	"github.com/devtron-labs/ongo/internal/vm"
)

// Janitor sweeps for expired sandboxes on an interval.
type Janitor struct {
	mgr      *vm.Manager
	interval time.Duration
}

func New(mgr *vm.Manager) *Janitor {
	return &Janitor{mgr: mgr, interval: 30 * time.Second}
}

// Run blocks until ctx is cancelled.
func (j *Janitor) Run(ctx context.Context) {
	t := time.NewTicker(j.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			j.sweep(ctx)
		}
	}
}

func (j *Janitor) sweep(ctx context.Context) {
	ids, err := j.mgr.ExpiredSandboxIDs(ctx, time.Now())
	if err != nil {
		log.Printf("janitor: list expired: %v", err)
		return
	}
	for _, id := range ids {
		if err := j.mgr.Delete(ctx, id, ""); err != nil {
			log.Printf("janitor: delete %s: %v", id, err)
			continue
		}
		log.Printf("janitor: deleted expired sandbox %s", id)
	}
}
