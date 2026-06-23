// Package vm translates between the public VM model and Kubernetes objects.
// A sandbox is a Pod; a persistent VM is a Deployment + PVC. All state is
// read back from the cluster via labels — there is no separate datastore.
package vm

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"

	"github.com/SWITCHin2/boxly/internal/k8s"
	"github.com/SWITCHin2/boxly/internal/template"
	"github.com/SWITCHin2/boxly/pkg/api"
)

// ErrNotFound is returned when a VM id matches no managed object.
var ErrNotFound = errors.New("vm not found")

// defaultOwner is the single tenant in the static-token MVP.
const defaultOwner = "default"

// Manager performs VM lifecycle operations against one namespace.
type Manager struct {
	client       *k8s.Client
	ns           string
	defaultImage string
	defaultTTL   time.Duration

	mu          sync.RWMutex
	pullSecrets []string // imagePullSecrets applied to every box (admin-tunable)
}

func NewManager(client *k8s.Client, namespace, defaultImage string, defaultTTL time.Duration, pullSecrets []string) *Manager {
	return &Manager{client: client, ns: namespace, defaultImage: defaultImage, defaultTTL: defaultTTL, pullSecrets: pullSecrets}
}

// Namespace returns the namespace VMs live in.
func (m *Manager) Namespace() string { return m.ns }

// PullSecrets / SetPullSecrets expose the runtime-tunable imagePullSecrets.
func (m *Manager) PullSecrets() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]string(nil), m.pullSecrets...)
}

func (m *Manager) SetPullSecrets(s []string) {
	m.mu.Lock()
	m.pullSecrets = append([]string(nil), s...)
	m.mu.Unlock()
}

// SetDefaults updates the default image / TTL at runtime (admin-tunable).
func (m *Manager) SetDefaults(image string, ttl time.Duration) {
	m.mu.Lock()
	if image != "" {
		m.defaultImage = image
	}
	if ttl > 0 {
		m.defaultTTL = ttl
	}
	m.mu.Unlock()
}

func (m *Manager) defaultImg() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.defaultImage
}

func (m *Manager) defaultTTLDur() time.Duration {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.defaultTTL
}

// Create provisions a VM. Sandboxes claim a warm pool pod when one is
// available (instant) and cold-create otherwise. Persistent VMs always get a
// fresh Deployment + PVC.
func (m *Manager) Create(ctx context.Context, req api.CreateRequest, owner string) (*api.VM, error) {
	if owner == "" {
		owner = defaultOwner
	}
	tmpl, _ := template.Get(req.Template)
	image := req.Image
	if image == "" {
		image = tmpl.Image
	}

	if req.Type == api.TypePersistent {
		return m.createPersistent(ctx, req, tmpl, image, owner)
	}

	// Sandbox path.
	ttl := m.defaultTTLDur()
	if req.TTLSeconds > 0 {
		ttl = time.Duration(req.TTLSeconds) * time.Second
	}
	expires := time.Now().Add(ttl)

	// Warm pools are now per-template and pre-prepared, so any template can be
	// claimed instantly when a ready warm box exists.
	claimed, err := m.claimWarm(ctx, req.Name, tmpl.ID, owner, expires)
	if err != nil {
		return nil, err
	}
	if claimed != nil {
		vm := toVMFromPod(claimed) // already prepared — instant
		return &vm, nil
	}

	// Cold path: no warm box available — create and prepare on the spot.
	id := genID()
	anns := map[string]string{AnnoExpiresAt: expires.Format(time.RFC3339)}
	built, err := BuildBox(m.ns, tmpl, id, req.Name, owner, PoolClaimed, m.PullSecrets(), anns)
	if err != nil {
		return nil, err
	}
	pod, err := m.pods().Create(ctx, built, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("create sandbox pod: %w", err)
	}
	m.prepare(ctx, pod.Name, tmpl)
	if fresh, err := m.pods().Get(ctx, pod.Name, metav1.GetOptions{}); err == nil {
		pod = fresh
	}
	vm := toVMFromPod(pod)
	return &vm, nil
}

func (m *Manager) createPersistent(ctx context.Context, req api.CreateRequest, tmpl template.Template, image, owner string) (*api.VM, error) {
	id := genID()
	pvc := BuildPVC(m.ns, id, owner)
	if _, err := m.client.Clientset.CoreV1().PersistentVolumeClaims(m.ns).Create(ctx, pvc, metav1.CreateOptions{}); err != nil {
		return nil, fmt.Errorf("create pvc: %w", err)
	}
	dep := BuildDeployment(m.ns, id, req.Name, image, owner, tmpl.ID, m.PullSecrets(), nil)
	created, err := m.client.Clientset.AppsV1().Deployments(m.ns).Create(ctx, dep, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("create deployment: %w", err)
	}
	if tmpl.SetupScript != "" {
		if name, err := m.waitRunningByVMID(ctx, id, 120*time.Second); err == nil {
			m.prepare(ctx, name, tmpl)
		}
	}
	vm := toVMFromDeployment(created)
	return &vm, nil
}

// prepare waits for the box to be Running and runs the template's setup script.
// Failures are logged but non-fatal — the box is still usable.
func (m *Manager) prepare(ctx context.Context, podName string, tmpl template.Template) {
	if tmpl.SetupScript == "" {
		return
	}
	if err := m.waitRunningPod(ctx, podName, 120*time.Second); err != nil {
		log.Printf("prepare %s: wait running: %v", podName, err)
		return
	}
	if err := m.runScript(ctx, podName, tmpl.SetupScript); err != nil {
		log.Printf("prepare %s: setup script: %v", podName, err)
	}
}

// List returns claimed sandboxes and persistent VMs (warm pool pods hidden).
// owner=="" lists every owner's boxes (admin); otherwise it is scoped.
func (m *Manager) List(ctx context.Context, owner string) ([]api.VM, error) {
	out := []api.VM{}

	podList, err := m.pods().List(ctx, metav1.ListOptions{
		LabelSelector: selector(append([]string{LabelType, string(api.TypeSandbox), LabelPool, PoolClaimed}, ownerSel(owner)...)...),
	})
	if err != nil {
		return nil, fmt.Errorf("list sandboxes: %w", err)
	}
	for i := range podList.Items {
		if podList.Items[i].DeletionTimestamp != nil {
			continue // already being deleted — don't show it
		}
		out = append(out, toVMFromPod(&podList.Items[i]))
	}

	depList, err := m.client.Clientset.AppsV1().Deployments(m.ns).List(ctx, metav1.ListOptions{
		LabelSelector: selector(append([]string{LabelType, string(api.TypePersistent)}, ownerSel(owner)...)...),
	})
	if err != nil {
		return nil, fmt.Errorf("list persistent: %w", err)
	}
	for i := range depList.Items {
		out = append(out, toVMFromDeployment(&depList.Items[i]))
	}
	return out, nil
}

// ownerSel returns label-selector pairs to scope by owner, or nil for admin.
func ownerSel(owner string) []string {
	if owner == "" {
		return nil
	}
	return []string{LabelOwner, owner}
}

// authorize returns ErrNotFound if owner is set and does not match the box's
// owner — so users can't see or touch other users' boxes.
func authorize(vmOwner, owner string) error {
	if owner != "" && vmOwner != owner {
		return ErrNotFound
	}
	return nil
}

// Get returns a single VM by id, scoped to owner ("" = admin/any).
func (m *Manager) Get(ctx context.Context, id, owner string) (*api.VM, error) {
	if pod, err := m.pods().Get(ctx, resourceName(id), metav1.GetOptions{}); err == nil {
		vm := toVMFromPod(pod)
		if err := authorize(vm.Owner, owner); err != nil {
			return nil, err
		}
		return &vm, nil
	} else if !apierrors.IsNotFound(err) {
		return nil, err
	}
	if dep, err := m.client.Clientset.AppsV1().Deployments(m.ns).Get(ctx, resourceName(id), metav1.GetOptions{}); err == nil {
		vm := toVMFromDeployment(dep)
		if err := authorize(vm.Owner, owner); err != nil {
			return nil, err
		}
		return &vm, nil
	} else if !apierrors.IsNotFound(err) {
		return nil, err
	}
	return nil, ErrNotFound
}

// Delete removes a VM and any backing PVC, scoped to owner ("" = admin/any).
func (m *Manager) Delete(ctx context.Context, id, owner string) error {
	if _, err := m.Get(ctx, id, owner); err != nil {
		return err // not found or not owned
	}
	name := resourceName(id)
	err := m.pods().Delete(ctx, name, metav1.DeleteOptions{})
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return err
	}
	// Persistent: delete deployment, then its PVC.
	derr := m.client.Clientset.AppsV1().Deployments(m.ns).Delete(ctx, name, metav1.DeleteOptions{})
	if apierrors.IsNotFound(derr) {
		return ErrNotFound
	}
	if derr != nil {
		return derr
	}
	if perr := m.client.Clientset.CoreV1().PersistentVolumeClaims(m.ns).Delete(ctx, name, metav1.DeleteOptions{}); perr != nil && !apierrors.IsNotFound(perr) {
		return perr
	}
	return nil
}

// PodNameForExec resolves the pod to exec into for a VM id. For sandboxes the
// pod name is deterministic; for persistent VMs we look up the running pod by
// label.
func (m *Manager) PodNameForExec(ctx context.Context, id, owner string) (string, error) {
	if pod, err := m.pods().Get(ctx, resourceName(id), metav1.GetOptions{}); err == nil {
		if err := authorize(pod.Labels[LabelOwner], owner); err != nil {
			return "", err
		}
		if pod.Status.Phase != corev1.PodRunning {
			return "", fmt.Errorf("vm %s is not running (%s)", id, pod.Status.Phase)
		}
		return pod.Name, nil
	} else if !apierrors.IsNotFound(err) {
		return "", err
	}
	list, err := m.pods().List(ctx, metav1.ListOptions{LabelSelector: selector(append([]string{LabelVMID, id}, ownerSel(owner)...)...)})
	if err != nil {
		return "", err
	}
	for i := range list.Items {
		if list.Items[i].Status.Phase == corev1.PodRunning {
			return list.Items[i].Name, nil
		}
	}
	return "", fmt.Errorf("no running pod for vm %s", id)
}

// claimWarm atomically claims a ready warm pod for the given template,
// returning nil if none exist. On a conflicting Update it tries the next one.
func (m *Manager) claimWarm(ctx context.Context, name, templateID, owner string, expires time.Time) (*corev1.Pod, error) {
	list, err := m.pods().List(ctx, metav1.ListOptions{
		LabelSelector: selector(LabelType, string(api.TypeSandbox), LabelPool, PoolWarm, LabelTemplate, templateID, LabelReady, "true"),
		FieldSelector: "status.phase=Running",
	})
	if err != nil {
		return nil, fmt.Errorf("list warm pods: %w", err)
	}
	for i := range list.Items {
		p := &list.Items[i]
		if p.DeletionTimestamp != nil {
			continue
		}
		p.Labels[LabelPool] = PoolClaimed
		p.Labels[LabelOwner] = owner
		if p.Annotations == nil {
			p.Annotations = map[string]string{}
		}
		p.Annotations[AnnoExpiresAt] = expires.Format(time.RFC3339)
		if name != "" {
			p.Annotations[AnnoName] = name
		}
		updated, err := m.pods().Update(ctx, p, metav1.UpdateOptions{})
		if err == nil {
			return updated, nil
		}
		if apierrors.IsConflict(err) {
			continue // lost the race; try the next warm pod
		}
		return nil, fmt.Errorf("claim warm pod: %w", err)
	}
	return nil, nil
}

// ListWarm returns all warm pods for a template (any phase).
func (m *Manager) ListWarm(ctx context.Context, templateID string) ([]corev1.Pod, error) {
	list, err := m.pods().List(ctx, metav1.ListOptions{
		LabelSelector: selector(LabelType, string(api.TypeSandbox), LabelPool, PoolWarm, LabelTemplate, templateID),
	})
	if err != nil {
		return nil, err
	}
	return list.Items, nil
}

// BoxPodName returns the underlying Kubernetes pod name for a box id (admin
// view), regardless of phase.
func (m *Manager) BoxPodName(ctx context.Context, id string) string {
	if pod, err := m.pods().Get(ctx, resourceName(id), metav1.GetOptions{}); err == nil {
		return pod.Name
	}
	if list, err := m.pods().List(ctx, metav1.ListOptions{LabelSelector: selector(LabelVMID, id)}); err == nil && len(list.Items) > 0 {
		return list.Items[0].Name
	}
	return resourceName(id)
}

// CountClaimed returns how many claimed (in-use) boxes a template has.
func (m *Manager) CountClaimed(ctx context.Context, templateID string) (int, error) {
	list, err := m.pods().List(ctx, metav1.ListOptions{
		LabelSelector: selector(LabelType, string(api.TypeSandbox), LabelPool, PoolClaimed, LabelTemplate, templateID),
	})
	if err != nil {
		return 0, err
	}
	return len(list.Items), nil
}

// CreateWarmPod adds one idle warm pod for the given template to the pool.
func (m *Manager) CreateWarmPod(ctx context.Context, tmpl template.Template) error {
	id := genID()
	if tmpl.Image == "" && tmpl.Manifest == "" {
		tmpl.Image = m.defaultImg()
	}
	pod, err := BuildBox(m.ns, tmpl, id, "", "", PoolWarm, m.PullSecrets(), nil)
	if err != nil {
		return err
	}
	_, err = m.pods().Create(ctx, pod, metav1.CreateOptions{})
	return err
}

// DeleteWarmPod removes a surplus warm pod.
func (m *Manager) DeleteWarmPod(ctx context.Context, name string) error {
	return m.pods().Delete(ctx, name, metav1.DeleteOptions{})
}

// PrepareWarm runs a warm pod's setup script (if any) then labels it ready so
// it can be claimed instantly. Safe to call repeatedly.
func (m *Manager) PrepareWarm(ctx context.Context, podName string, tmpl template.Template) error {
	if tmpl.SetupScript != "" {
		if err := m.runScript(ctx, podName, tmpl.SetupScript); err != nil {
			return fmt.Errorf("setup: %w", err)
		}
	}
	patch := []byte(`{"metadata":{"labels":{"` + LabelReady + `":"true"}}}`)
	_, err := m.pods().Patch(ctx, podName, types.MergePatchType, patch, metav1.PatchOptions{})
	return err
}

// ExpiredSandboxIDs returns sandbox VM ids whose TTL has passed.
func (m *Manager) ExpiredSandboxIDs(ctx context.Context, now time.Time) ([]string, error) {
	list, err := m.pods().List(ctx, metav1.ListOptions{
		LabelSelector: selector(LabelType, string(api.TypeSandbox), LabelPool, PoolClaimed),
	})
	if err != nil {
		return nil, err
	}
	var ids []string
	for i := range list.Items {
		exp := list.Items[i].Annotations[AnnoExpiresAt]
		if exp == "" {
			continue
		}
		if t, err := time.Parse(time.RFC3339, exp); err == nil && now.After(t) {
			ids = append(ids, list.Items[i].Labels[LabelVMID])
		}
	}
	return ids, nil
}

func (m *Manager) pods() corev1client.PodInterface { return m.client.Clientset.CoreV1().Pods(m.ns) }

func genID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
