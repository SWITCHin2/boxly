package vm

import (
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/devtron-labs/ongo/pkg/api"
)

func toVMFromPod(p *corev1.Pod) api.VM {
	vm := api.VM{
		ID:        p.Labels[LabelVMID],
		Name:      p.Annotations[AnnoName],
		Type:      api.VMType(p.Labels[LabelType]),
		Template:  p.Labels[LabelTemplate],
		Owner:     p.Labels[LabelOwner],
		Pool:      p.Labels[LabelPool],
		Status:    string(p.Status.Phase),
		CreatedAt: p.CreationTimestamp.Time,
	}
	if len(p.Spec.Containers) > 0 {
		vm.Image = p.Spec.Containers[0].Image
	}
	if exp := p.Annotations[AnnoExpiresAt]; exp != "" {
		if t, err := time.Parse(time.RFC3339, exp); err == nil {
			vm.ExpiresAt = &t
		}
	}
	return vm
}

func toVMFromDeployment(d *appsv1.Deployment) api.VM {
	vm := api.VM{
		ID:        d.Labels[LabelVMID],
		Name:      d.Annotations[AnnoName],
		Type:      api.TypePersistent,
		Template:  d.Labels[LabelTemplate],
		Owner:     d.Labels[LabelOwner],
		Status:    "Pending",
		CreatedAt: d.CreationTimestamp.Time,
	}
	if d.Status.ReadyReplicas >= 1 {
		vm.Status = "Running"
	}
	if len(d.Spec.Template.Spec.Containers) > 0 {
		vm.Image = d.Spec.Template.Spec.Containers[0].Image
	}
	return vm
}
