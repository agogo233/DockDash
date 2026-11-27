package discovery

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"dockdash/internal/repo"
)

func TestParseKubeconfig(t *testing.T) {
	data := parseKubeconfig([]byte(`
apiVersion: v1
clusters:
- cluster:
    server: https://cluster.example
    certificate-authority-data: Y2E=
  name: prod
contexts:
- context:
    cluster: prod
    user: admin
  name: prod-admin
current-context: prod-admin
kind: Config
users:
- name: admin
  user:
    token: secret
`))
	if data.currentContext != "prod-admin" {
		t.Fatalf("unexpected current context: %q", data.currentContext)
	}
	cluster, ok := findKubeconfigEntry(data.clusters, "prod")
	if !ok || cluster.fields["cluster.server"] != "https://cluster.example" {
		t.Fatalf("unexpected cluster: %#v", data.clusters)
	}
	contextEntry, ok := findKubeconfigEntry(data.contexts, "prod-admin")
	if !ok || contextEntry.fields["context.cluster"] != "prod" || contextEntry.fields["context.user"] != "admin" {
		t.Fatalf("unexpected context: %#v", data.contexts)
	}
	user, ok := findKubeconfigEntry(data.users, "admin")
	if !ok || user.fields["user.token"] != "secret" {
		t.Fatalf("unexpected user: %#v", data.users)
	}
}

func TestKubernetesHealthAndScan(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/namespaces/default/pods" {
			http.NotFound(writer, request)
			return
		}
		if request.Header.Get("Authorization") != "Bearer secret" {
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"items":[{"metadata":{"name":"web-abc1234567","uid":"uid-1","ownerReferences":[{"kind":"ReplicaSet","name":"web-abc1234567","controller":true}]},"spec":{"containers":[{"name":"web","image":"nginx:1.25","ports":[{"containerPort":80}]}]},"status":{"phase":"Running","podIP":"10.1.2.3","containerStatuses":[{"name":"web","containerID":"containerd://abc","imageID":"docker-pullable://nginx@sha256:digest","ready":true}]}}]}`))
	}))
	defer server.Close()

	client := buildKubeClient("prod-admin", kubeconfigEntry{
		fields: map[string]string{"cluster.server": server.URL},
	}, kubeconfigEntry{
		fields: map[string]string{"user.token": "secret"},
	})
	kubernetes := &Kubernetes{clients: []*kubeClient{client}, namespaces: []string{"default"}}

	health := kubernetes.Health(context.Background())
	if len(health) != 1 || !health[0].Connected || health[0].Pods == nil || *health[0].Pods != 1 {
		t.Fatalf("unexpected health: %#v", health)
	}

	services := []repo.Service{}
	if err := kubernetes.Scan(context.Background(), func(service repo.Service) {
		services = append(services, service)
	}); err != nil {
		t.Fatal(err)
	}
	if len(services) != 1 {
		t.Fatalf("expected one service, got %d", len(services))
	}
	service := services[0]
	if service.Source != "kubernetes" || service.Status != "up" || service.Name != "default/web:web" {
		t.Fatalf("unexpected service: %#v", service)
	}
	if service.Metadata["kubernetesContext"] != "prod-admin" || service.Metadata["workloadKind"] != "Deployment" || service.Metadata["workloadName"] != "web" {
		t.Fatalf("unexpected metadata: %#v", service.Metadata)
	}
	if service.Metadata["image"] != "nginx" || service.Metadata["imageTag"] != "1.25" || service.Metadata["imageDigest"] != "sha256:digest" {
		t.Fatalf("unexpected image metadata: %#v", service.Metadata)
	}
	if kubernetes.SourceName(service) != "prod-admin" {
		t.Fatalf("unexpected source name: %q", kubernetes.SourceName(service))
	}
}
