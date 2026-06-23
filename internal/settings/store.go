// Package settings holds boxlyd's runtime-tunable configuration: pool bounds,
// decay, defaults, image pull secrets and custom templates. Values are seeded
// from env at boot, editable live via the admin API, and persisted to a
// ConfigMap so they survive restarts.
package settings

import (
	"sync"

	"github.com/SWITCHin2/boxly/internal/template"
)

// Settings is the full editable configuration (JSON-serialized into the ConfigMap).
type Settings struct {
	PoolMin           int                 `json:"poolMin"`
	PoolMax           int                 `json:"poolMax"`
	PoolDecay         float64             `json:"poolDecay"`
	DefaultTTLSeconds int                 `json:"defaultTtlSeconds"`
	DefaultImage      string              `json:"defaultImage"`
	PullSecrets       []string            `json:"pullSecrets"`     // existing secret names to reference
	PullSecretYAML    string              `json:"pullSecretYaml"`  // a k8s Secret manifest Boxly applies
	Templates         []template.Template `json:"templates"`       // custom (non-builtin) only
	Users             []User              `json:"users"`           // per-user API tokens (multi-tenant)
	DefaultUserDays   int                 `json:"defaultUserDays"` // onboarding validity for a new user
	MaxUserDays       int                 `json:"maxUserDays"`     // cap on user validity (0 = unlimited)
}

// User is an API identity. A request bearing Token is scoped to owner Name.
// ExpiresAt is the onboarding expiry (RFC3339); empty = never expires.
type User struct {
	Name      string `json:"name"`
	Token     string `json:"token"`
	ExpiresAt string `json:"expiresAt,omitempty"`
}

// Store is a thread-safe holder for Settings. Set persists then applies the new
// values to the live system via the configured hooks.
type Store struct {
	mu    sync.RWMutex
	cur   Settings
	save  func(Settings) error // persist (e.g. to ConfigMap); may be nil
	apply func(Settings)       // push values into the running system
}

// NewStore creates a store and applies the initial settings immediately.
func NewStore(initial Settings, save func(Settings) error, apply func(Settings)) *Store {
	st := &Store{cur: initial, save: save, apply: apply}
	if st.apply != nil {
		st.apply(initial)
	}
	return st
}

// Get returns a copy of the current settings.
func (s *Store) Get() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c := s.cur
	c.PullSecrets = append([]string(nil), s.cur.PullSecrets...)
	c.Templates = append([]template.Template(nil), s.cur.Templates...)
	return c
}

// Set validates, stores, persists, and applies new settings.
func (s *Store) Set(next Settings) error {
	next.normalize()
	s.mu.Lock()
	s.cur = next
	s.mu.Unlock()

	if s.save != nil {
		if err := s.save(next); err != nil {
			return err
		}
	}
	if s.apply != nil {
		s.apply(next)
	}
	return nil
}

func (s *Settings) normalize() {
	if s.PoolMin < 0 {
		s.PoolMin = 0
	}
	if s.PoolMax < s.PoolMin {
		s.PoolMax = s.PoolMin
	}
	if s.PoolDecay <= 0 || s.PoolDecay >= 1 {
		s.PoolDecay = 0.7
	}
	if s.DefaultUserDays <= 0 {
		s.DefaultUserDays = 5
	}
	if s.MaxUserDays < 0 {
		s.MaxUserDays = 0
	}
}
