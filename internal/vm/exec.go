package vm

import (
	"bytes"
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
)

// runScript executes a POSIX sh setup script inside the box and waits for it to
// finish. Used to prepare prebaked templates after the box is Running.
func (m *Manager) runScript(ctx context.Context, podName, script string) error {
	req := m.client.Clientset.CoreV1().RESTClient().Post().
		Resource("pods").Name(podName).Namespace(m.ns).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: "vm",
			Command:   []string{"/bin/sh", "-c", script},
			Stdout:    true,
			Stderr:    true,
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(m.client.RestConfig, "POST", req.URL())
	if err != nil {
		return fmt.Errorf("build executor: %w", err)
	}
	var stdout, stderr bytes.Buffer
	if err := exec.StreamWithContext(ctx, remotecommand.StreamOptions{Stdout: &stdout, Stderr: &stderr}); err != nil {
		return fmt.Errorf("setup failed: %w (%s)", err, stderr.String())
	}
	return nil
}

// waitRunningPod polls until the named pod reaches Running or the timeout hits.
func (m *Manager) waitRunningPod(ctx context.Context, podName string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		pod, err := m.pods().Get(ctx, podName, metav1.GetOptions{})
		if err == nil && pod.Status.Phase == corev1.PodRunning {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("pod %s not running within %s", podName, timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// waitRunningByVMID polls until a pod with the given vm-id is Running and
// returns its name (used for persistent VMs whose pod name is generated).
func (m *Manager) waitRunningByVMID(ctx context.Context, id string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for {
		if name, err := m.PodNameForExec(ctx, id, ""); err == nil {
			return name, nil
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("no running pod for vm %s within %s", id, timeout)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(time.Second):
		}
	}
}
