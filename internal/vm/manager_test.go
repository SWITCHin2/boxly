package vm

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/devtron-labs/ongo/internal/k8s"
	"github.com/devtron-labs/ongo/internal/template"
	"github.com/devtron-labs/ongo/pkg/api"
)

func newTestManager() *Manager {
	cs := fake.NewSimpleClientset()
	return NewManager(&k8s.Client{Clientset: cs}, "ongo", "ubuntu:24.04", time.Hour, nil)
}

func TestSandboxColdCreateListDelete(t *testing.T) {
	ctx := context.Background()
	m := newTestManager()

	vm, err := m.Create(ctx, api.CreateRequest{Type: api.TypeSandbox, Name: "demo"}, "default")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if vm.ID == "" || vm.Type != api.TypeSandbox || vm.Pool != PoolClaimed {
		t.Fatalf("unexpected vm: %+v", vm)
	}

	list, err := m.List(ctx, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].ID != vm.ID || list[0].Name != "demo" {
		t.Fatalf("list mismatch: %+v", list)
	}

	if err := m.Delete(ctx, vm.ID, ""); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if list, _ := m.List(ctx, ""); len(list) != 0 {
		t.Fatalf("expected empty after delete, got %+v", list)
	}
}

func TestWarmPoolClaimIsInstant(t *testing.T) {
	ctx := context.Background()
	m := newTestManager()

	// Seed a warm pod for the "normal" template, mark it Running + ready (the
	// fake clientset won't schedule or prepare it for us).
	tmpl, _ := template.Get("normal")
	if err := m.CreateWarmPod(ctx, tmpl); err != nil {
		t.Fatalf("warm: %v", err)
	}
	pods, _ := m.pods().List(ctx, metav1.ListOptions{})
	warmID := pods.Items[0].Labels[LabelVMID]
	pods.Items[0].Status.Phase = corev1.PodRunning
	pods.Items[0].Labels[LabelReady] = "true"
	if _, err := m.pods().Update(ctx, &pods.Items[0], metav1.UpdateOptions{}); err != nil {
		t.Fatalf("set running: %v", err)
	}

	vm, err := m.Create(ctx, api.CreateRequest{Type: api.TypeSandbox}, "default")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Claim reuses the warm pod's identity rather than creating a new one.
	if vm.ID != warmID {
		t.Fatalf("expected claimed warm pod %s, got %s", warmID, vm.ID)
	}
	if vm.Pool != PoolClaimed {
		t.Fatalf("expected claimed, got %q", vm.Pool)
	}
	warm, _ := m.ListWarm(ctx, "normal")
	if len(warm) != 0 {
		t.Fatalf("expected 0 warm after claim, got %d", len(warm))
	}
}

func TestExpiredSandboxIDs(t *testing.T) {
	ctx := context.Background()
	m := newTestManager()

	vm, err := m.Create(ctx, api.CreateRequest{Type: api.TypeSandbox, TTLSeconds: 1}, "default")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	expired, err := m.ExpiredSandboxIDs(ctx, time.Now().Add(2*time.Second))
	if err != nil {
		t.Fatalf("expired: %v", err)
	}
	if len(expired) != 1 || expired[0] != vm.ID {
		t.Fatalf("expected %s expired, got %+v", vm.ID, expired)
	}
}

func TestPersistentCreatesDeploymentAndPVC(t *testing.T) {
	ctx := context.Background()
	m := newTestManager()

	vm, err := m.Create(ctx, api.CreateRequest{Type: api.TypePersistent, Name: "vps"}, "default")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if vm.Type != api.TypePersistent {
		t.Fatalf("unexpected type: %+v", vm)
	}
	if _, err := m.client.Clientset.AppsV1().Deployments("ongo").Get(ctx, resourceName(vm.ID), metav1.GetOptions{}); err != nil {
		t.Fatalf("deployment missing: %v", err)
	}
	if _, err := m.client.Clientset.CoreV1().PersistentVolumeClaims("ongo").Get(ctx, resourceName(vm.ID), metav1.GetOptions{}); err != nil {
		t.Fatalf("pvc missing: %v", err)
	}

	if err := m.Delete(ctx, vm.ID, ""); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := m.client.Clientset.CoreV1().PersistentVolumeClaims("ongo").Get(ctx, resourceName(vm.ID), metav1.GetOptions{}); err == nil {
		t.Fatalf("expected pvc deleted")
	}
}

func TestHardenedPodSpec(t *testing.T) {
	pod := BuildPod("ongo", "abcd1234", "x", "ubuntu:24.04", api.TypeSandbox, "default", PoolClaimed, "normal", nil, nil)
	sc := pod.Spec.SecurityContext
	if sc == nil || sc.RunAsNonRoot == nil || !*sc.RunAsNonRoot {
		t.Fatal("pod must run as non-root")
	}
	if pod.Spec.AutomountServiceAccountToken == nil || *pod.Spec.AutomountServiceAccountToken {
		t.Fatal("service account token must not be automounted")
	}
	csc := pod.Spec.Containers[0].SecurityContext
	if csc.AllowPrivilegeEscalation == nil || *csc.AllowPrivilegeEscalation {
		t.Fatal("privilege escalation must be disabled")
	}
	if len(csc.Capabilities.Drop) == 0 || csc.Capabilities.Drop[0] != "ALL" {
		t.Fatal("all capabilities must be dropped")
	}
}
