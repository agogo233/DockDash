package discovery

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"dockdash/internal/config"
	"dockdash/internal/repo"
)

type ClusterHealth struct {
	Context    string `json:"context"`
	Connected  bool   `json:"connected"`
	Namespaces *int   `json:"namespaces,omitempty"`
	Pods       *int   `json:"pods,omitempty"`
	Error      string `json:"error,omitempty"`
}

type kubeClient struct {
	context string
	id      string
	server  string
	token   string
	http    *http.Client
	err     error
}

type Kubernetes struct {
	clients    []*kubeClient
	namespaces []string
}

type podList struct {
	Items []pod `json:"items"`
}

type pod struct {
	Metadata struct {
		Name            string     `json:"name"`
		UID             string     `json:"uid"`
		OwnerReferences []ownerRef `json:"ownerReferences"`
	} `json:"metadata"`
	Spec struct {
		Containers []container `json:"containers"`
	} `json:"spec"`
	Status struct {
		Phase             string            `json:"phase"`
		PodIP             string            `json:"podIP"`
		ContainerStatuses []containerStatus `json:"containerStatuses"`
	} `json:"status"`
}

type ownerRef struct {
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Controller bool   `json:"controller"`
}

type container struct {
	Name  string `json:"name"`
	Image string `json:"image"`
	Ports []struct {
		ContainerPort int `json:"containerPort"`
	} `json:"ports"`
}

type containerStatus struct {
	Name        string `json:"name"`
	ContainerID string `json:"containerID"`
	ImageID     string `json:"imageID"`
	Ready       bool   `json:"ready"`
	State       struct {
		Terminated json.RawMessage `json:"terminated"`
	} `json:"state"`
}

var replicaSetSuffix = regexp.MustCompile(`-[a-f0-9]{8,10}$`)

func NewKubernetes(cfg *config.Config) *Kubernetes {
	namespaces := append([]string{}, cfg.KubernetesNamespaces...)
	if len(namespaces) == 0 {
		namespaces = []string{"default"}
	}
	result := &Kubernetes{namespaces: namespaces}
	if cfg.KubernetesEnabled != "true" {
		return result
	}
	if path := firstExistingFile(cfg.KubernetesKubeconfig); path != "" {
		content, err := os.ReadFile(path)
		if err == nil {
			result.clients = clientsFromKubeconfig(content, cfg.KubernetesContexts)
			return result
		}
	}
	if client, err := inClusterClient(); err == nil {
		result.clients = append(result.clients, client)
		return result
	}
	if path := defaultKubeconfigPath(); path != "" {
		content, err := os.ReadFile(path)
		if err == nil {
			result.clients = clientsFromKubeconfig(content, cfg.KubernetesContexts)
		}
	}
	return result
}

func firstExistingFile(path string) string {
	if path == "" {
		return ""
	}
	if _, err := os.Stat(path); err == nil {
		return path
	}
	return ""
}

func defaultKubeconfigPath() string {
	for _, path := range filepath.SplitList(os.Getenv("KUBECONFIG")) {
		if existing := firstExistingFile(path); existing != "" {
			return existing
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return firstExistingFile(home + "/.kube/config")
}

func clientsFromKubeconfig(content []byte, contexts []string) []*kubeClient {
	data := parseKubeconfig(content)
	selected := contexts
	if len(selected) == 0 && data.currentContext != "" {
		selected = []string{data.currentContext}
	}
	clients := make([]*kubeClient, 0, len(selected))
	for _, contextName := range selected {
		contextEntry, ok := findKubeconfigEntry(data.contexts, contextName)
		if !ok {
			clients = append(clients, &kubeClient{
				context: contextName,
				id:      clientID(contextName),
				err:     fmt.Errorf("kubeconfig context %q not found", contextName),
			})
			continue
		}
		clusterName := contextEntry.fields["context.cluster"]
		userName := contextEntry.fields["context.user"]
		clusterEntry, ok := findKubeconfigEntry(data.clusters, clusterName)
		if !ok {
			clients = append(clients, &kubeClient{
				context: contextName,
				id:      clientID(contextName),
				err:     fmt.Errorf("kubeconfig cluster %q not found", clusterName),
			})
			continue
		}
		userEntry, ok := findKubeconfigEntry(data.users, userName)
		if !ok {
			clients = append(clients, &kubeClient{
				context: contextName,
				id:      clientID(contextName),
				err:     fmt.Errorf("kubeconfig user %q not found", userName),
			})
			continue
		}
		clients = append(clients, buildKubeClient(contextName, clusterEntry, userEntry))
	}
	return clients
}

func buildKubeClient(contextName string, cluster, user kubeconfigEntry) *kubeClient {
	client := &kubeClient{
		context: contextName,
		id:      clientID(contextName),
		server:  strings.TrimRight(cluster.fields["cluster.server"], "/"),
		token:   firstNonEmpty(user.fields["user.token"], readFileString(user.fields["user.tokenFile"])),
	}
	if client.server == "" {
		client.err = fmt.Errorf("kubeconfig context %q has no API server", contextName)
		return client
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if cluster.fields["cluster.insecure-skip-tls-verify"] == "true" {
		tlsConfig.InsecureSkipVerify = true
	}
	ca := readMaterial(cluster.fields["cluster.certificate-authority-data"], cluster.fields["cluster.certificate-authority"])
	if len(ca) > 0 {
		pool := x509.NewCertPool()
		if pool.AppendCertsFromPEM(ca) {
			tlsConfig.RootCAs = pool
		}
	}
	cert := readMaterial(user.fields["user.client-certificate-data"], user.fields["user.client-certificate"])
	key := readMaterial(user.fields["user.client-key-data"], user.fields["user.client-key"])
	if len(cert) > 0 && len(key) > 0 {
		pair, err := tls.X509KeyPair(cert, key)
		if err == nil {
			tlsConfig.Certificates = []tls.Certificate{pair}
		}
	}
	client.http = &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{TLSClientConfig: tlsConfig}}
	return client
}

func inClusterClient() (*kubeClient, error) {
	host := os.Getenv("KUBERNETES_SERVICE_HOST")
	port := os.Getenv("KUBERNETES_SERVICE_PORT")
	if host == "" || port == "" {
		return nil, fmt.Errorf("Kubernetes service environment is not available")
	}
	base := "/var/run/secrets/kubernetes.io/serviceaccount/"
	token, err := os.ReadFile(base + "token")
	if err != nil {
		return nil, err
	}
	ca, err := os.ReadFile(base + "ca.crt")
	if err != nil {
		return nil, err
	}
	contextName := strings.TrimSpace(os.Getenv("KUBERNETES_CONTEXT"))
	if contextName == "" {
		contextName = "in-cluster"
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		return nil, fmt.Errorf("invalid Kubernetes service account CA")
	}
	return &kubeClient{
		context: contextName,
		id:      clientID(contextName),
		server:  "https://" + net.JoinHostPort(host, port),
		token:   strings.TrimSpace(string(token)),
		http: &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{
			TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool},
		}},
	}, nil
}

func clientID(contextName string) string {
	digest := sha256.Sum256([]byte(contextName))
	return hex.EncodeToString(digest[:])[:16]
}

func (k *Kubernetes) Configured() bool { return len(k.clients) > 0 }

func (k *Kubernetes) SourceName(service repo.Service) string {
	if name, ok := service.Metadata["kubernetesContext"].(string); ok {
		return name
	}
	return ""
}

func (k *Kubernetes) Health(ctx context.Context) []ClusterHealth {
	result := make([]ClusterHealth, 0, len(k.clients))
	for _, client := range k.clients {
		health := ClusterHealth{Context: client.context}
		if client.err != nil {
			health.Error = client.err.Error()
			result = append(result, health)
			continue
		}
		pods := 0
		failed := false
		for _, namespace := range k.namespaces {
			list, err := client.listPods(ctx, namespace)
			if err != nil {
				health.Error = err.Error()
				failed = true
				break
			}
			pods += len(list.Items)
		}
		if failed {
			result = append(result, health)
			continue
		}
		health.Connected = true
		health.Namespaces = intPointer(len(k.namespaces))
		health.Pods = intPointer(pods)
		result = append(result, health)
	}
	return result
}

func (k *Kubernetes) Scan(ctx context.Context, emit func(repo.Service)) error {
	for _, client := range k.clients {
		if client.err != nil {
			return client.err
		}
		for _, namespace := range k.namespaces {
			list, err := client.listPods(ctx, namespace)
			if err != nil {
				return err
			}
			for _, item := range list.Items {
				if err := ctx.Err(); err != nil {
					return err
				}
				if item.Metadata.Name == "" || item.Metadata.UID == "" ||
					item.Status.Phase == "Succeeded" || item.Status.Phase == "Failed" {
					continue
				}
				for _, container := range item.Spec.Containers {
					emit(buildService(client, namespace, item, container))
				}
			}
		}
	}
	return nil
}

func buildService(client *kubeClient, namespace string, item pod, container container) repo.Service {
	ports := []int{}
	for _, port := range container.Ports {
		if port.ContainerPort > 0 {
			ports = append(ports, port.ContainerPort)
		}
	}
	protocol := "tcp"
	if len(ports) > 0 {
		protocol = detectProtocol(ports[0])
	}
	checkPort := 0
	if len(ports) > 0 {
		checkPort = ports[0]
	}

	owner := ownerRef{Kind: "Pod", Name: item.Metadata.Name}
	for _, reference := range item.Metadata.OwnerReferences {
		if reference.Controller {
			owner = reference
			break
		}
	}
	if len(item.Metadata.OwnerReferences) > 0 && owner.Kind == "Pod" {
		owner = item.Metadata.OwnerReferences[0]
	}
	workloadKind := owner.Kind
	workloadName := owner.Name
	if workloadKind == "ReplicaSet" {
		workloadKind = "Deployment"
		workloadName = replicaSetSuffix.ReplaceAllString(workloadName, "")
	}

	image := strings.Split(container.Image, "@")[0]
	imageName := image
	imageTag := "latest"
	if last := image[strings.LastIndex(image, "/")+1:]; last != "" {
		if colon := strings.LastIndex(last, ":"); colon >= 0 {
			imageTag = last[colon+1:]
			imageName = image[:len(image)-len(last)+colon]
		}
	}

	status := "unknown"
	containerID := ""
	imageDigest := ""
	for _, state := range item.Status.ContainerStatuses {
		if state.Name != container.Name {
			continue
		}
		containerID = state.ContainerID
		if state.Ready {
			status = "up"
		} else if len(state.State.Terminated) > 0 && string(state.State.Terminated) != "null" {
			status = "down"
		}
		if parts := strings.Split(state.ImageID, "@"); len(parts) > 1 {
			imageDigest = parts[1]
		}
	}

	host := item.Status.PodIP
	if host == "" {
		host = item.Metadata.Name + "." + namespace + ".pod"
	}
	now := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	metadata := map[string]any{
		"clusterId":         client.id,
		"kubernetesContext": client.context,
		"namespace":         namespace,
		"podUid":            item.Metadata.UID,
		"podName":           item.Metadata.Name,
		"containerName":     container.Name,
		"workloadKind":      workloadKind,
		"workloadName":      workloadName,
		"image":             imageName,
		"imageTag":          imageTag,
	}
	if containerID != "" {
		metadata["containerId"] = containerID
	}
	if imageDigest != "" {
		metadata["imageDigest"] = imageDigest
	}

	service := repo.Service{
		ID:        "kubernetes-" + repo.NewID(),
		Name:      namespace + "/" + workloadName + ":" + container.Name,
		Host:      host,
		Protocol:  &protocol,
		Ports:     ports,
		Source:    "kubernetes",
		Status:    status,
		Metadata:  metadata,
		Labels:    []string{},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if len(ports) > 0 {
		service.CheckPort = &checkPort
	}
	return service
}

func (c *kubeClient) listPods(ctx context.Context, namespace string) (podList, error) {
	path := "/api/v1/namespaces/" + url.PathEscape(namespace) + "/pods"
	result := podList{}
	err := c.request(ctx, path, &result)
	return result, err
}

func (c *kubeClient) request(ctx context.Context, path string, target any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.server+path, nil)
	if err != nil {
		return err
	}
	if c.token != "" {
		request.Header.Set("Authorization", "Bearer "+c.token)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	body := io.LimitReader(response.Body, 1<<20)
	if response.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(body)
		return fmt.Errorf("Kubernetes API returned %s: %s", response.Status, strings.TrimSpace(string(message)))
	}
	return json.NewDecoder(body).Decode(target)
}

func readMaterial(dataValue, fileValue string) []byte {
	if strings.TrimSpace(dataValue) != "" {
		if decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(dataValue)); err == nil {
			return decoded
		}
		return []byte(dataValue)
	}
	if fileValue != "" {
		data, err := os.ReadFile(fileValue)
		if err == nil {
			return data
		}
	}
	return nil
}

func readFileString(path string) string {
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func intPointer(value int) *int { return &value }
