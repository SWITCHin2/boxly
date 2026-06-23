// Package api holds the wire types shared between the ongod control plane
// and the ongo CLI client. It must not import internal packages so that
// pkg/client (used by the CLI) can depend on it.
package api

import "time"

// VMType selects the lifecycle model of a VM.
type VMType string

const (
	// TypeSandbox is an ephemeral single Pod that auto-expires via TTL.
	TypeSandbox VMType = "sandbox"
	// TypePersistent is a Deployment + PVC whose workspace survives restarts.
	TypePersistent VMType = "persistent"
)

// VM is the public representation of a managed VM. It is derived entirely
// from Kubernetes objects (labels/annotations + pod status); there is no DB.
type VM struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Type      VMType     `json:"type"`
	Template  string     `json:"template,omitempty"`
	Image     string     `json:"image"`
	Status    string     `json:"status"`
	Owner     string     `json:"owner"`
	Pool      string     `json:"pool,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
}

// CreateRequest is the body of POST /v1/vms.
type CreateRequest struct {
	Type       VMType `json:"type"`
	Template   string `json:"template,omitempty"`
	Image      string `json:"image,omitempty"`
	TTLSeconds int    `json:"ttlSeconds,omitempty"`
	Name       string `json:"name,omitempty"`
}

// TemplateInfo is a launchable box type shown in the picker (GET /v1/templates).
type TemplateInfo struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Desc     string `json:"desc"`
	Category string `json:"category"`
}

// Identity describes the authenticated user (GET /v1/me).
type Identity struct {
	Owner         string     `json:"owner"`
	ExpiresAt     *time.Time `json:"expiresAt,omitempty"`
	DaysRemaining int        `json:"daysRemaining"` // -1 = unlimited
}

// ErrorResponse is returned for non-2xx responses.
type ErrorResponse struct {
	Error string `json:"error"`
}
