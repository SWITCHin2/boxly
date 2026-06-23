// Package config loads runtime configuration for both binaries from
// environment variables, with sensible defaults for local minikube dev.
package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Server holds the ongod control-plane configuration. Env values seed the
// defaults; many are overridable at runtime via the admin console (ConfigMap).
type Server struct {
	Addr            string        // listen address, e.g. ":8080"
	Token           string        // bearer token for the user API
	AdminToken      string        // bearer token for the admin API
	Namespace       string        // namespace VMs are created in
	PoolMin         int           // global floor for warm boxes per template
	PoolMax         int           // global ceiling for warm boxes per template
	PoolDecay       float64       // EWMA history retention per reconcile tick
	DefaultImage    string        // base image for the "normal" box
	DefaultTTL      time.Duration // default sandbox TTL
	PullSecrets     []string      // imagePullSecrets applied to every box
	DefaultUserDays int           // onboarding validity for a new user
	MaxUserDays     int           // cap on user validity (0 = unlimited)
	Kubeconfig      string        // explicit kubeconfig path (optional)
}

// LoadServer reads the ongod configuration from the environment.
func LoadServer() Server {
	return Server{
		Addr:            env("BOXLY_ADDR", ":8080"),
		Token:           env("BOXLY_TOKEN", "dev-secret"),
		AdminToken:      env("BOXLY_ADMIN_TOKEN", "admin-secret"),
		Namespace:       env("BOXLY_NAMESPACE", "ongo"),
		PoolMin:         envInt("BOXLY_POOL_MIN", 0),
		PoolMax:         envInt("BOXLY_POOL_MAX", 5),
		PoolDecay:       envFloat("BOXLY_POOL_DECAY", 0.7),
		DefaultImage:    env("BOXLY_DEFAULT_IMAGE", "ubuntu:24.04"),
		DefaultTTL:      envDuration("BOXLY_DEFAULT_TTL", time.Hour),
		PullSecrets:     envList("BOXLY_PULL_SECRETS"),
		DefaultUserDays: envInt("BOXLY_USER_DAYS", 5),
		MaxUserDays:     envInt("BOXLY_MAX_USER_DAYS", 30),
		Kubeconfig:      os.Getenv("KUBECONFIG"),
	}
}

// CLI holds the ongo client configuration.
type CLI struct {
	Server string // base URL of ongod, e.g. http://localhost:8080
	Token  string // bearer token
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envFloat(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

func envList(key string) []string {
	v := os.Getenv(key)
	if v == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func envDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
