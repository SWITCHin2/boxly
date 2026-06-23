package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/yaml"
)

// ConfigMapName is where runtime settings are persisted.
const ConfigMapName = "ongo-config"

const settingsKey = "settings.json"

// Load reads settings from the ConfigMap. Returns ok=false if it doesn't exist
// yet (caller should fall back to env-seeded defaults).
func Load(ctx context.Context, cs kubernetes.Interface, namespace string) (Settings, bool, error) {
	cm, err := cs.CoreV1().ConfigMaps(namespace).Get(ctx, ConfigMapName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return Settings{}, false, nil
	}
	if err != nil {
		return Settings{}, false, err
	}
	var s Settings
	if raw := cm.Data[settingsKey]; raw != "" {
		if err := json.Unmarshal([]byte(raw), &s); err != nil {
			return Settings{}, false, err
		}
		return s, true, nil
	}
	return Settings{}, false, nil
}

// ApplyPullSecret parses a k8s Secret YAML, applies it (create or update) into
// the namespace, and returns its name so boxes can reference it.
func ApplyPullSecret(ctx context.Context, cs kubernetes.Interface, namespace, secretYAML string) (string, error) {
	if strings.TrimSpace(secretYAML) == "" {
		return "", nil
	}
	var sec corev1.Secret
	if err := yaml.Unmarshal([]byte(secretYAML), &sec); err != nil {
		return "", fmt.Errorf("parse secret yaml: %w", err)
	}
	if sec.Name == "" {
		return "", fmt.Errorf("secret yaml has no metadata.name")
	}
	sec.Namespace = namespace
	_, err := cs.CoreV1().Secrets(namespace).Update(ctx, &sec, metav1.UpdateOptions{})
	if apierrors.IsNotFound(err) {
		_, err = cs.CoreV1().Secrets(namespace).Create(ctx, &sec, metav1.CreateOptions{})
	}
	if err != nil {
		return "", err
	}
	return sec.Name, nil
}

// Saver returns a save func that upserts the settings ConfigMap.
func Saver(cs kubernetes.Interface, namespace string) func(Settings) error {
	return func(s Settings) error {
		raw, err := json.Marshal(s)
		if err != nil {
			return err
		}
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      ConfigMapName,
				Namespace: namespace,
				Labels:    map[string]string{"ongo.dev/managed": "true"},
			},
			Data: map[string]string{settingsKey: string(raw)},
		}
		ctx := context.Background()
		_, err = cs.CoreV1().ConfigMaps(namespace).Update(ctx, cm, metav1.UpdateOptions{})
		if apierrors.IsNotFound(err) {
			_, err = cs.CoreV1().ConfigMaps(namespace).Create(ctx, cm, metav1.CreateOptions{})
		}
		return err
	}
}
