// Package server implements the boxlyd control-plane HTTP API.
package server

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log"
	"math"
	"net/http"
	"time"

	"github.com/SWITCHin2/boxly/internal/auth"
	"github.com/SWITCHin2/boxly/internal/demand"
	"github.com/SWITCHin2/boxly/internal/k8s"
	"github.com/SWITCHin2/boxly/internal/settings"
	"github.com/SWITCHin2/boxly/internal/template"
	"github.com/SWITCHin2/boxly/internal/vm"
	"github.com/SWITCHin2/boxly/pkg/api"
)

// Server holds the dependencies shared by the HTTP handlers.
type Server struct {
	mgr        *vm.Manager
	k8s        *k8s.Client
	token      string
	adminToken string
	tracker    *demand.Tracker
	store      *settings.Store
}

func New(mgr *vm.Manager, k8sClient *k8s.Client, token, adminToken string, tracker *demand.Tracker, store *settings.Store) *Server {
	return &Server{mgr: mgr, k8s: k8sClient, token: token, adminToken: adminToken, tracker: tracker, store: store}
}

// Handler builds the routed http.Handler. /healthz is unauthenticated; every
// /v1 route requires the static bearer token.
func (s *Server) Handler() http.Handler {
	apiMux := http.NewServeMux()
	apiMux.HandleFunc("POST /v1/vms", s.handleCreate)
	apiMux.HandleFunc("GET /v1/me", s.handleMe)
	apiMux.HandleFunc("GET /v1/templates", s.handleTemplates)
	apiMux.HandleFunc("GET /v1/vms", s.handleList)
	apiMux.HandleFunc("GET /v1/vms/{id}", s.handleGet)
	apiMux.HandleFunc("DELETE /v1/vms/{id}", s.handleDelete)
	apiMux.HandleFunc("GET /v1/vms/{id}/exec", s.handleExec)

	// Admin JSON API — protected by the separate admin token.
	adminMux := http.NewServeMux()
	adminMux.HandleFunc("GET /v1/admin/pools", s.handleAdminPools)
	adminMux.HandleFunc("GET /v1/admin/boxes", s.handleAdminBoxes)
	adminMux.HandleFunc("DELETE /v1/admin/boxes/{id}", s.handleAdminDeleteBox)
	adminMux.HandleFunc("GET /v1/admin/users", s.handleAdminUsers)
	adminMux.HandleFunc("GET /v1/admin/config", s.handleAdminGetConfig)
	adminMux.HandleFunc("PUT /v1/admin/config", s.handleAdminPutConfig)
	adminMux.HandleFunc("GET /v1/admin/templates", s.handleAdminGetTemplates)
	adminMux.HandleFunc("PUT /v1/admin/templates", s.handleAdminPutTemplates)
	adminMux.HandleFunc("GET /v1/admin/templates/{id}/manifest", s.handleAdminManifest)
	adminMux.HandleFunc("POST /v1/admin/render", s.handleAdminRender)

	root := http.NewServeMux()
	root.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	// Static admin page is public (it asks for the token, then calls the API).
	root.HandleFunc("GET /admin", s.handleAdminPage)
	root.HandleFunc("GET /admin/logo", s.handleAdminLogo)
	root.HandleFunc("GET /admin/stats", s.handleAdminStats) // public aggregate counts for the login
	// Public, unauthenticated live-pools page for normal users.
	root.HandleFunc("GET /pools", s.handlePublicPoolsPage)
	root.HandleFunc("GET /pools-data", s.handlePublicPools)
	root.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/pools", http.StatusFound) })
	root.Handle("/v1/admin/", auth.Middleware(s.adminToken)(adminMux))
	root.Handle("/v1/", auth.Resolve(s.resolveUser)(apiMux))
	return root
}

// resolveUser maps a bearer token to an owner: the legacy global token → the
// "default" user, and any configured per-user token → that user's name.
func (s *Server) resolveUser(token string) (string, bool) {
	if token == "" {
		return "", false
	}
	if subtle.ConstantTimeCompare([]byte(token), []byte(s.token)) == 1 {
		return "default", true
	}
	for _, u := range s.store.Get().Users {
		if u.Token != "" && subtle.ConstantTimeCompare([]byte(token), []byte(u.Token)) == 1 {
			if u.ExpiresAt != "" {
				if t, err := time.Parse(time.RFC3339, u.ExpiresAt); err == nil && time.Now().After(t) {
					return "", false // onboarding window expired
				}
			}
			return u.Name, true
		}
	}
	return "", false
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	owner := auth.Owner(r.Context())
	id := api.Identity{Owner: owner, DaysRemaining: -1}
	for _, u := range s.store.Get().Users {
		if u.Name == owner && u.ExpiresAt != "" {
			if t, err := time.Parse(time.RFC3339, u.ExpiresAt); err == nil {
				id.ExpiresAt = &t
				d := int(math.Ceil(time.Until(t).Hours() / 24))
				if d < 0 {
					d = 0
				}
				id.DaysRemaining = d
			}
		}
	}
	writeJSON(w, http.StatusOK, id)
}

func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request) {
	var req api.CreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	if req.Type == "" {
		req.Type = api.TypeSandbox
	}
	if req.Type != api.TypeSandbox && req.Type != api.TypePersistent {
		writeErr(w, http.StatusBadRequest, "type must be sandbox or persistent")
		return
	}
	// Record demand against the resolved template so the pool can pre-warm it.
	tmpl, _ := template.Get(req.Template)
	s.tracker.Record(tmpl.ID)

	vmObj, err := s.mgr.Create(r.Context(), req, auth.Owner(r.Context()))
	if err != nil {
		log.Printf("create: %v", err)
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, vmObj)
}

func (s *Server) handleTemplates(w http.ResponseWriter, _ *http.Request) {
	ts := template.All()
	out := make([]api.TemplateInfo, 0, len(ts))
	for _, t := range ts {
		out = append(out, api.TemplateInfo{ID: t.ID, Title: t.Title, Desc: t.Desc, Category: t.Category})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	vms, err := s.mgr.List(r.Context(), auth.Owner(r.Context()))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, vms)
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	vmObj, err := s.mgr.Get(r.Context(), r.PathValue("id"), auth.Owner(r.Context()))
	if errors.Is(err, vm.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "vm not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, vmObj)
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	err := s.mgr.Delete(r.Context(), r.PathValue("id"), auth.Owner(r.Context()))
	if errors.Is(err, vm.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "vm not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, api.ErrorResponse{Error: msg})
}
