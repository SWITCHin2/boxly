// Package demand tracks per-template create demand with an exponentially
// weighted moving average, so the pool can pre-warm boxes ahead of need.
package demand

import (
	"math"
	"sync"
)

// Tracker keeps an EWMA of create requests per template. Each reconcile cycle
// calls Tick to fold the requests seen during the interval into the average;
// idle templates decay toward zero. It is safe for concurrent use.
type Tracker struct {
	mu      sync.Mutex
	decay   float64 // 0..1 retention of the previous average per Tick
	ewma    map[string]float64
	pending map[string]float64
}

// New returns a tracker. decay is the weight kept from history each Tick
// (e.g. 0.7 → fairly responsive). Out-of-range values clamp to a sane default.
func New(decay float64) *Tracker {
	if decay <= 0 || decay >= 1 {
		decay = 0.7
	}
	return &Tracker{decay: decay, ewma: map[string]float64{}, pending: map[string]float64{}}
}

// SetDecay updates the EWMA history retention at runtime (admin-tunable).
func (t *Tracker) SetDecay(decay float64) {
	if decay <= 0 || decay >= 1 {
		return
	}
	t.mu.Lock()
	t.decay = decay
	t.mu.Unlock()
}

// Record notes one create request for a template.
func (t *Tracker) Record(id string) {
	if id == "" {
		return
	}
	t.mu.Lock()
	t.pending[id]++
	t.mu.Unlock()
}

// Tick folds the interval's requests into each EWMA and resets the counters.
// Templates that drop near zero are forgotten.
func (t *Tracker) Tick() {
	t.mu.Lock()
	defer t.mu.Unlock()
	seen := map[string]bool{}
	for id, n := range t.pending {
		t.ewma[id] = t.decay*t.ewma[id] + (1-t.decay)*n
		seen[id] = true
	}
	t.pending = map[string]float64{}
	for id := range t.ewma {
		if !seen[id] {
			t.ewma[id] *= t.decay // no requests this interval → decay
		}
		if t.ewma[id] < 0.05 {
			delete(t.ewma, id)
		}
	}
}

// Rate returns the current smoothed demand for a template (requests per tick).
func (t *Tracker) Rate(id string) float64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.ewma[id]
}

// Desired returns how many warm boxes to keep for a template: the smoothed
// demand, rounded up, clamped to [min, max].
func (t *Tracker) Desired(id string, min, max int) int {
	d := int(math.Ceil(t.Rate(id)))
	if d < min {
		d = min
	}
	if max > 0 && d > max {
		d = max
	}
	return d
}
