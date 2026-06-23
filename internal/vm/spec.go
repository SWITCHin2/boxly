package vm

import (
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"

	"github.com/devtron-labs/ongo/internal/template"
	"github.com/devtron-labs/ongo/pkg/api"
)

// workspaceMount is where the writable workspace lives inside every VM.
// For sandboxes it is an emptyDir; for persistent VMs it is a PVC.
const workspaceMount = "/work"

// resourceName is the deterministic k8s object name for a VM id.
func resourceName(id string) string { return "boxly-" + id }

func baseLabels(id string, typ api.VMType, owner, pool, templateID string) map[string]string {
	l := map[string]string{
		LabelManaged: "true",
		LabelVMID:    id,
		LabelType:    string(typ),
		LabelOwner:   owner,
	}
	if pool != "" {
		l[LabelPool] = pool
	}
	if templateID != "" {
		l[LabelTemplate] = templateID
	}
	return l
}

// ptrBool / ptrInt64 are small helpers for the *T fields k8s specs use.
func ptrBool(b bool) *bool    { return &b }
func ptrInt64(i int64) *int64 { return &i }

// podSecurityContext / containerSecurityContext implement the hardening
// described in the plan: non-root, no privilege escalation, all caps dropped,
// RuntimeDefault seccomp.
func podSecurityContext() *corev1.PodSecurityContext {
	return &corev1.PodSecurityContext{
		RunAsNonRoot: ptrBool(true),
		RunAsUser:    ptrInt64(1000),
		RunAsGroup:   ptrInt64(1000),
		FSGroup:      ptrInt64(1000),
		SeccompProfile: &corev1.SeccompProfile{
			Type: corev1.SeccompProfileTypeRuntimeDefault,
		},
	}
}

func containerSecurityContext() *corev1.SecurityContext {
	return &corev1.SecurityContext{
		AllowPrivilegeEscalation: ptrBool(false),
		RunAsNonRoot:             ptrBool(true),
		Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
	}
}

func resources() corev1.ResourceRequirements {
	return corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("100m"),
			corev1.ResourceMemory: resource.MustParse("128Mi"),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("500m"),
			corev1.ResourceMemory: resource.MustParse("512Mi"),
		},
	}
}

// vmContainer builds the single long-lived container that keeps the VM alive
// so we can exec into it. workspaceVolume is mounted at /work as $HOME.
func vmContainer(image string) corev1.Container {
	return corev1.Container{
		Name:            "vm",
		Image:           image,
		Command:         []string{"sleep", "infinity"},
		WorkingDir:      workspaceMount,
		Env:             []corev1.EnvVar{{Name: "HOME", Value: workspaceMount}},
		Resources:       resources(),
		SecurityContext: containerSecurityContext(),
		VolumeMounts: []corev1.VolumeMount{
			{Name: "workspace", MountPath: workspaceMount},
		},
	}
}

// BuildPod constructs a sandbox pod (also used for warm pool pods, where
// owner is empty, pool=warm and templateID is empty).
func BuildPod(namespace, id, name, image string, typ api.VMType, owner, pool, templateID string, pullSecrets []string, anns map[string]string) *corev1.Pod {
	pod := &corev1.Pod{Spec: corev1.PodSpec{Containers: []corev1.Container{vmContainer(image)}}}
	hardenPod(pod, namespace, id, name, typ, owner, pool, templateID, pullSecrets, anns)
	return pod
}

// BuildBox builds a sandbox pod for a template. When the template has a free-hand
// Manifest it is parsed and used as the base; otherwise a generated spec is used.
// Either way hardenPod re-enforces all safety-critical fields.
func BuildBox(namespace string, tmpl template.Template, id, name, owner, pool string, pullSecrets []string, anns map[string]string) (*corev1.Pod, error) {
	if strings.TrimSpace(tmpl.Manifest) == "" {
		return BuildPod(namespace, id, name, tmpl.Image, api.TypeSandbox, owner, pool, tmpl.ID, pullSecrets, anns), nil
	}
	var pod corev1.Pod
	if err := yaml.Unmarshal([]byte(tmpl.Manifest), &pod); err != nil {
		return nil, fmt.Errorf("parse template manifest: %w", err)
	}
	hardenPod(&pod, namespace, id, name, api.TypeSandbox, owner, pool, tmpl.ID, pullSecrets, anns)
	return &pod, nil
}

// RenderManifest returns the YAML a template currently resolves to — the
// admin's starting point for free-hand edits in the UI.
func RenderManifest(namespace string, tmpl template.Template) (string, error) {
	pod, err := BuildBox(namespace, tmpl, "TEMPLATE", "", "", PoolWarm, nil, nil)
	if err != nil {
		return "", err
	}
	pod.ManagedFields = nil
	out, err := yaml.Marshal(pod)
	return string(out), err
}

// hardenPod enforces OnGo's non-negotiable fields on any pod (generated or
// free-hand), leaving the user's image/command/env/resources/extra-volumes alone.
func hardenPod(pod *corev1.Pod, namespace, id, name string, typ api.VMType, owner, pool, templateID string, pullSecrets []string, anns map[string]string) {
	// Identity & metadata.
	pod.TypeMeta = metav1.TypeMeta{APIVersion: "v1", Kind: "Pod"}
	pod.Name = resourceName(id)
	pod.Namespace = namespace
	if pod.Labels == nil {
		pod.Labels = map[string]string{}
	}
	for k, v := range baseLabels(id, typ, owner, pool, templateID) {
		pod.Labels[k] = v
	}
	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}
	for k, v := range withName(anns, name) {
		pod.Annotations[k] = v
	}

	// Pod-level security & identity.
	pod.Spec.ServiceAccountName = vmServiceAccount
	pod.Spec.AutomountServiceAccountToken = ptrBool(false)
	pod.Spec.SecurityContext = podSecurityContext()
	if pod.Spec.RestartPolicy == "" {
		pod.Spec.RestartPolicy = corev1.RestartPolicyAlways
	}
	pod.Spec.ImagePullSecrets = pullSecretRefs(pullSecrets)

	// Ensure the workspace volume exists.
	hasWS := false
	for _, v := range pod.Spec.Volumes {
		if v.Name == "workspace" {
			hasWS = true
		}
	}
	if !hasWS {
		pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{
			Name: "workspace", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
		})
	}

	// At least one container, named "vm" (the one we exec into) and hardened.
	if len(pod.Spec.Containers) == 0 {
		pod.Spec.Containers = []corev1.Container{vmContainer("ubuntu:24.04")}
	}
	hasVM := false
	for i := range pod.Spec.Containers {
		if pod.Spec.Containers[i].Name == "vm" {
			hasVM = true
		}
	}
	for i := range pod.Spec.Containers {
		c := &pod.Spec.Containers[i]
		if !hasVM && i == 0 {
			c.Name = "vm"
		}
		c.SecurityContext = containerSecurityContext()
		if len(c.Command) == 0 && len(c.Args) == 0 {
			c.Command = []string{"sleep", "infinity"}
		}
		if c.WorkingDir == "" {
			c.WorkingDir = workspaceMount
		}
		ensureMount(c)
		ensureHomeEnv(c)
	}
}

func ensureMount(c *corev1.Container) {
	for _, m := range c.VolumeMounts {
		if m.Name == "workspace" {
			return
		}
	}
	c.VolumeMounts = append(c.VolumeMounts, corev1.VolumeMount{Name: "workspace", MountPath: workspaceMount})
}

func ensureHomeEnv(c *corev1.Container) {
	for _, e := range c.Env {
		if e.Name == "HOME" {
			return
		}
	}
	c.Env = append(c.Env, corev1.EnvVar{Name: "HOME", Value: workspaceMount})
}

func pullSecretRefs(names []string) []corev1.LocalObjectReference {
	if len(names) == 0 {
		return nil
	}
	refs := make([]corev1.LocalObjectReference, 0, len(names))
	for _, n := range names {
		refs = append(refs, corev1.LocalObjectReference{Name: n})
	}
	return refs
}

// BuildPVC constructs the persistent workspace claim for a persistent VM.
func BuildPVC(namespace, id, owner string) *corev1.PersistentVolumeClaim {
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      resourceName(id),
			Namespace: namespace,
			Labels:    baseLabels(id, api.TypePersistent, owner, "", ""),
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("1Gi")},
			},
		},
	}
}

// BuildDeployment constructs a persistent VM backed by the PVC built above.
func BuildDeployment(namespace, id, name, image, owner, templateID string, pullSecrets []string, anns map[string]string) *appsv1.Deployment {
	labels := baseLabels(id, api.TypePersistent, owner, "", templateID)
	one := int32(1)
	c := vmContainer(image)
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        resourceName(id),
			Namespace:   namespace,
			Labels:      labels,
			Annotations: withName(anns, name),
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &one,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{LabelVMID: id}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					ServiceAccountName:           vmServiceAccount,
					AutomountServiceAccountToken: ptrBool(false),
					SecurityContext:              podSecurityContext(),
					ImagePullSecrets:             pullSecretRefs(pullSecrets),
					Containers:                   []corev1.Container{c},
					Volumes: []corev1.Volume{
						{Name: "workspace", VolumeSource: corev1.VolumeSource{
							PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: resourceName(id)},
						}},
					},
				},
			},
		},
	}
}

func withName(anns map[string]string, name string) map[string]string {
	out := map[string]string{}
	for k, v := range anns {
		out[k] = v
	}
	if name != "" {
		out[AnnoName] = name
	}
	return out
}

// selector builds a label selector string for listing managed objects.
func selector(extra ...string) string {
	s := LabelManaged + "=true"
	for i := 0; i+1 < len(extra); i += 2 {
		s += fmt.Sprintf(",%s=%s", extra[i], extra[i+1])
	}
	return s
}
