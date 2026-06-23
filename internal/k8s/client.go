// Package k8s wires up a Kubernetes client. It prefers in-cluster config
// (when boxlyd runs as a pod) and falls back to a kubeconfig file for local
// development against minikube.
package k8s

import (
	"fmt"
	"os"
	"path/filepath"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Client bundles a clientset with the REST config, which the exec bridge needs
// to build a SPDY/websocket executor. Clientset is an interface so tests can
// inject a fake.
type Client struct {
	Clientset  kubernetes.Interface
	RestConfig *rest.Config
}

// New builds a client, trying in-cluster config first and then the given
// kubeconfig path (or the standard KUBECONFIG / ~/.kube/config locations).
func New(kubeconfig string) (*Client, error) {
	cfg, err := loadConfig(kubeconfig)
	if err != nil {
		return nil, err
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("build clientset: %w", err)
	}
	return &Client{Clientset: cs, RestConfig: cfg}, nil
}

func loadConfig(kubeconfig string) (*rest.Config, error) {
	if cfg, err := rest.InClusterConfig(); err == nil {
		return cfg, nil
	}
	path := kubeconfig
	if path == "" {
		path = os.Getenv("KUBECONFIG")
	}
	if path == "" {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, ".kube", "config")
		}
	}
	cfg, err := clientcmd.BuildConfigFromFlags("", path)
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig %q: %w", path, err)
	}
	return cfg, nil
}
