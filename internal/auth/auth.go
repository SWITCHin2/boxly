// Package auth provides bearer-token authentication and per-user identity.
package auth

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"
)

type ctxKey int

const ownerKey ctxKey = 0

// WithOwner stores the resolved owner on the context.
func WithOwner(ctx context.Context, owner string) context.Context {
	return context.WithValue(ctx, ownerKey, owner)
}

// Owner returns the authenticated owner for the request ("" if none/admin).
func Owner(ctx context.Context) string {
	o, _ := ctx.Value(ownerKey).(string)
	return o
}

// Middleware requires a single static token (used for the admin API).
func Middleware(token string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got := bearer(r)
			if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// Resolve authenticates the user API: it maps the bearer token to an owner via
// the supplied resolver and stores it on the context.
func Resolve(resolve func(token string) (owner string, ok bool)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			owner, ok := resolve(bearer(r))
			if !ok {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r.WithContext(WithOwner(r.Context(), owner)))
		})
	}
}

// bearer extracts the token from the Authorization header, falling back to the
// websocket subprotocol ("bearer.<token>") for browser/WS clients.
func bearer(r *http.Request) string {
	if t := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "); t != "" {
		return t
	}
	for _, p := range strings.Split(r.Header.Get("Sec-WebSocket-Protocol"), ",") {
		if p = strings.TrimSpace(p); strings.HasPrefix(p, "bearer.") {
			return strings.TrimPrefix(p, "bearer.")
		}
	}
	return ""
}
