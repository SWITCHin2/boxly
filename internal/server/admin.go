package server

import (
	_ "embed"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/devtron-labs/ongo/internal/settings"
	"github.com/devtron-labs/ongo/internal/template"
	"github.com/devtron-labs/ongo/internal/vm"
	"github.com/devtron-labs/ongo/pkg/api"
)

//go:embed admin/index.html
var adminHTML []byte

//go:embed admin/logo.png
var adminLogo []byte

func (s *Server) handleAdminPage(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(adminHTML)
}

func (s *Server) handleAdminLogo(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(adminLogo)
}

type poolStat struct {
	ID      string  `json:"id"`
	Title   string  `json:"title"`
	Image   string  `json:"image"`
	Warm    int     `json:"warm"`    // waiting boxes (any phase)
	Ready   int     `json:"ready"`   // warm boxes prepared & claimable
	Claimed int     `json:"claimed"` // in-use boxes
	Desired int     `json:"desired"` // predicted target
	Rate    float64 `json:"rate"`    // EWMA demand
}

// poolStats computes the per-template warm-pool snapshot (no sensitive data).
func (s *Server) poolStats(r *http.Request) ([]poolStat, error) {
	cfg := s.store.Get()
	out := []poolStat{}
	for _, t := range template.AllIncludingDisabled() {
		warmPods, err := s.mgr.ListWarm(r.Context(), t.ID)
		if err != nil {
			return nil, err
		}
		ready := 0
		for i := range warmPods {
			if warmPods[i].Labels[vm.LabelReady] == "true" {
				ready++
			}
		}
		claimed, _ := s.mgr.CountClaimed(r.Context(), t.ID)
		out = append(out, poolStat{
			ID: t.ID, Title: t.Title, Image: t.Image,
			Warm: len(warmPods), Ready: ready, Claimed: claimed,
			Desired: s.tracker.Desired(t.ID, effMin(t, cfg.PoolMin), effMax(t, cfg.PoolMax)),
			Rate:    s.tracker.Rate(t.ID),
		})
	}
	return out, nil
}

func (s *Server) handleAdminPools(w http.ResponseWriter, r *http.Request) {
	out, err := s.poolStats(r)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

//go:embed admin/pools.html
var poolsHTML []byte

// handlePublicPoolsPage serves the public live-pools page (no auth).
func (s *Server) handlePublicPoolsPage(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(poolsHTML)
}

// handlePublicPools serves aggregate pool stats publicly (no names, no box ids).
func (s *Server) handlePublicPools(w http.ResponseWriter, r *http.Request) {
	out, err := s.poolStats(r)
	if err != nil {
		writeJSON(w, http.StatusOK, []poolStat{})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// adminConfig is the editable runtime config (excludes templates, which have
// their own endpoint).
type adminConfig struct {
	PoolMin           int             `json:"poolMin"`
	PoolMax           int             `json:"poolMax"`
	PoolDecay         float64         `json:"poolDecay"`
	DefaultTTLSeconds int             `json:"defaultTtlSeconds"`
	DefaultImage      string          `json:"defaultImage"`
	PullSecrets       []string        `json:"pullSecrets"`
	PullSecretYAML    string          `json:"pullSecretYaml"`
	Users             []settings.User `json:"users"`
	DefaultUserDays   int             `json:"defaultUserDays"`
	MaxUserDays       int             `json:"maxUserDays"`
	Namespace         string          `json:"namespace"` // read-only (set at install)
}

func (s *Server) handleAdminGetConfig(w http.ResponseWriter, _ *http.Request) {
	c := s.store.Get()
	writeJSON(w, http.StatusOK, adminConfig{
		PoolMin: c.PoolMin, PoolMax: c.PoolMax, PoolDecay: c.PoolDecay,
		DefaultTTLSeconds: c.DefaultTTLSeconds, DefaultImage: c.DefaultImage,
		PullSecrets: c.PullSecrets, PullSecretYAML: c.PullSecretYAML, Users: c.Users,
		DefaultUserDays: c.DefaultUserDays, MaxUserDays: c.MaxUserDays,
		Namespace: s.mgr.Namespace(),
	})
}

func (s *Server) handleAdminPutConfig(w http.ResponseWriter, r *http.Request) {
	var in adminConfig
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	cur := s.store.Get()
	cur.PoolMin, cur.PoolMax, cur.PoolDecay = in.PoolMin, in.PoolMax, in.PoolDecay
	cur.DefaultTTLSeconds, cur.DefaultImage = in.DefaultTTLSeconds, in.DefaultImage
	cur.PullSecrets, cur.PullSecretYAML = in.PullSecrets, in.PullSecretYAML
	cur.Users = in.Users
	cur.DefaultUserDays, cur.MaxUserDays = in.DefaultUserDays, in.MaxUserDays
	if err := s.store.Set(cur); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleAdminStats is a small PUBLIC summary for the login screen — aggregate
// counts only, no names or sensitive data.
func (s *Server) handleAdminStats(w http.ResponseWriter, r *http.Request) {
	vms, err := s.mgr.List(r.Context(), "")
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]int{"instances": 0, "owners": 0, "types": len(template.All())})
		return
	}
	owners := map[string]bool{}
	for _, v := range vms {
		if v.Owner != "" {
			owners[v.Owner] = true
		}
	}
	writeJSON(w, http.StatusOK, map[string]int{
		"instances": len(vms), "owners": len(owners), "types": len(template.All()),
	})
}

// adminBox is an individual box enriched with its k8s pod name (admin view).
type adminBox struct {
	api.VM
	PodName string `json:"podName"`
}

// handleAdminBoxes lists every individual box across all users (admin view).
func (s *Server) handleAdminBoxes(w http.ResponseWriter, r *http.Request) {
	vms, err := s.mgr.List(r.Context(), "") // "" = all owners
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]adminBox, 0, len(vms))
	for _, v := range vms {
		out = append(out, adminBox{VM: v, PodName: s.mgr.BoxPodName(r.Context(), v.ID)})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleAdminDeleteBox force-stops any box (admin override — ignores ownership).
func (s *Server) handleAdminDeleteBox(w http.ResponseWriter, r *http.Request) {
	err := s.mgr.Delete(r.Context(), r.PathValue("id"), "") // "" = admin, any owner
	if errors.Is(err, vm.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "box not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type userStat struct {
	Name    string `json:"name"`
	Boxes   int    `json:"boxes"`
	Tokened bool   `json:"tokened"` // has a configured API token
}

// handleAdminUsers summarises configured users + ad-hoc owners with live box counts.
func (s *Server) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	vms, err := s.mgr.List(r.Context(), "")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	counts := map[string]int{}
	for _, v := range vms {
		counts[v.Owner]++
	}
	seen := map[string]bool{}
	out := []userStat{}
	for _, u := range s.store.Get().Users {
		out = append(out, userStat{Name: u.Name, Boxes: counts[u.Name], Tokened: true})
		seen[u.Name] = true
	}
	for owner, c := range counts {
		if owner != "" && !seen[owner] {
			out = append(out, userStat{Name: owner, Boxes: c})
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// handleAdminManifest returns the YAML a template resolves to (its free-hand
// override if set, otherwise the generated manifest as a starting point).
func (s *Server) handleAdminManifest(w http.ResponseWriter, r *http.Request) {
	tmpl, ok := template.Get(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "template not found")
		return
	}
	y := tmpl.Manifest
	if strings.TrimSpace(y) == "" {
		rendered, err := vm.RenderManifest(s.mgr.Namespace(), tmpl)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		y = rendered
	}
	writeJSON(w, http.StatusOK, map[string]string{"manifest": y})
}

// handleAdminRender renders the generated manifest for a (possibly unsaved)
// template definition posted from the editor, so "Load generated" works before
// the template is saved.
func (s *Server) handleAdminRender(w http.ResponseWriter, r *http.Request) {
	var t template.Template
	if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	t.Manifest = "" // always render the generated spec, not an existing override
	if strings.TrimSpace(t.Image) == "" {
		if di := s.store.Get().DefaultImage; di != "" {
			t.Image = di
		} else {
			t.Image = template.BaseImage
		}
	}
	if t.ID == "" {
		t.ID = "new"
	}
	y, err := vm.RenderManifest(s.mgr.Namespace(), t)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"manifest": y})
}

func (s *Server) handleAdminGetTemplates(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, template.AllIncludingDisabled())
}

func (s *Server) handleAdminPutTemplates(w http.ResponseWriter, r *http.Request) {
	var in []template.Template
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	cur := s.store.Get()
	cur.Templates = in
	if err := s.store.Set(cur); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// effMin / effMax mirror the pool package's bounds logic for the desired count.
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
