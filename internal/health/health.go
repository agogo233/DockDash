package health

import (
	"context"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"dockdash/internal/notify"
	"dockdash/internal/registry"
	"dockdash/internal/repo"
)

const (
	StatusUp      = "up"
	StatusDown    = "down"
	StatusUnknown = "unknown"
)

const probeTimeout = time.Second

type DockerScanner interface {
	Scan(ctx context.Context) ([]repo.Service, error)
}

type Checker struct {
	repo        *repo.Repo
	docker      DockerScanner
	notifier    *notify.Client
	locale      string
	history     bool
	concurrency int
}

type statusChange struct {
	id       string
	name     string
	previous string
	current  string
}

func NewChecker(database *repo.Repo, scanner DockerScanner, notifier *notify.Client, locale string, history bool) *Checker {
	return &Checker{repo: database, docker: scanner, notifier: notifier, locale: locale,
		history: history, concurrency: 20}
}

func (c *Checker) CheckAll(ctx context.Context) error {
	services, err := c.repo.GetServices()
	if err != nil {
		return err
	}
	snapshot := map[string]map[string]repo.Service{}
	if c.docker != nil {
		discovered, scanErr := c.docker.Scan(ctx)
		if scanErr != nil {
			log.Printf("Health check: docker snapshot failed: %v", scanErr)
		} else {
			for _, service := range discovered {
				containerName, ok := service.Metadata["containerName"].(string)
				if !ok {
					continue
				}
				hostID, _ := service.Metadata["dockerHostId"].(string)
				if snapshot[hostID] == nil {
					snapshot[hostID] = map[string]repo.Service{}
				}
				snapshot[hostID][containerName] = service
			}
		}
	}
	changes := make([]statusChange, len(services))
	semaphore := make(chan struct{}, c.concurrency)
	var waitGroup sync.WaitGroup
	for index, service := range services {
		if ctx.Err() != nil {
			break
		}
		waitGroup.Add(1)
		semaphore <- struct{}{}
		go func(index int, service repo.Service) {
			defer waitGroup.Done()
			defer func() { <-semaphore }()
			status := c.checkService(ctx, service, snapshot)
			changes[index] = statusChange{id: service.ID, name: service.Name, previous: service.Status, current: status}
		}(index, service)
	}
	waitGroup.Wait()

	notifiable := []statusChange{}
	for _, change := range changes {
		if change.current == "" {
			continue
		}
		if err := c.repo.UpdateServiceStatus(change.id, change.current); err != nil {
			log.Printf("Health check: failed to update status for %q: %v", change.name, err)
			continue
		}
		if c.history {
			if err := c.repo.AddHealthHistory(change.id, change.current); err != nil {
				log.Printf("Health check: failed to record history for %q: %v", change.name, err)
			}
		}
		if change.previous != change.current {
			notifiable = append(notifiable, change)
		}
	}
	c.notifyChanges(ctx, notifiable)
	return nil
}

func (c *Checker) checkService(ctx context.Context, service repo.Service, snapshot map[string]map[string]repo.Service) string {
	switch service.Source {
	case "docker":
		return c.checkDockerService(service, snapshot)
	case "kubernetes":
		return StatusUnknown
	default:
		return c.checkNetworkService(ctx, service)
	}
}

func (c *Checker) checkDockerService(service repo.Service, snapshot map[string]map[string]repo.Service) string {
	hostID, _ := service.Metadata["dockerHostId"].(string)
	containerName, _ := service.Metadata["containerName"].(string)
	hostSnapshot, ok := snapshot[hostID]
	if !ok {
		return StatusUnknown
	}
	current, ok := hostSnapshot[containerName]
	if !ok {
		return StatusDown
	}
	status := current.Status
	if status != StatusUp && status != StatusDown {
		status = StatusUnknown
	}
	patch := map[string]any{}
	if containerID, ok := current.Metadata["containerId"].(string); ok {
		patch["containerId"] = containerID
	}
	imageTag, tagOK := current.Metadata["imageTag"].(string)
	if tagOK {
		patch["imageTag"] = imageTag
	}
	if digest, ok := current.Metadata["imageDigest"].(string); ok {
		patch["imageDigest"] = digest
	}
	if hasUpdate, ok := service.Metadata["hasUpdate"].(bool); ok && hasUpdate && tagOK {
		previousTag, _ := service.Metadata["imageTag"].(string)
		latestVersion, _ := service.Metadata["latestVersion"].(string)
		if updateApplied(previousTag, imageTag, latestVersion) {
			patch["hasUpdate"] = false
			patch["latestVersion"] = ""
		}
	}
	if len(patch) > 0 {
		if err := c.repo.UpdateServiceMetadata(service.ID, patch); err != nil {
			log.Printf("Health check: failed to refresh metadata for %q: %v", service.Name, err)
		}
	}
	return status
}

func updateApplied(previousTag, newTag, latestVersion string) bool {
	if newTag == previousTag {
		return false
	}
	newParsed := registry.ExtractSemVer(newTag)
	latestParsed := registry.ExtractSemVer(latestVersion)
	if newParsed == nil || latestParsed == nil {
		return false
	}
	return registry.CompareSemVer(newParsed, latestParsed) >= 0
}

func (c *Checker) checkNetworkService(ctx context.Context, service repo.Service) string {
	if service.CheckPort == nil || service.Host == "" {
		return StatusUnknown
	}
	port := *service.CheckPort
	protocol := "tcp"
	if service.Protocol != nil {
		protocol = *service.Protocol
	} else {
		protocol = detectProtocol(port)
	}
	if protocol == "http" || protocol == "https" {
		client := &http.Client{Timeout: probeTimeout}
		target := protocol + "://" + net.JoinHostPort(service.Host, strconv.Itoa(port))
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err == nil {
			request.Header.Set("User-Agent", "DockDash/1.0")
			response, requestErr := client.Do(request)
			if requestErr == nil {
				_ = response.Body.Close()
				if response.StatusCode < 500 {
					return StatusUp
				}
			}
		}
	}
	address := net.JoinHostPort(service.Host, strconv.Itoa(port))
	connection, err := (&net.Dialer{Timeout: probeTimeout}).DialContext(ctx, "tcp", address)
	if err != nil {
		return StatusDown
	}
	_ = connection.Close()
	return StatusUp
}

func detectProtocol(port int) string {
	switch port {
	case 80, 8080, 3000, 3001, 5000, 8000:
		return "http"
	case 443, 8443:
		return "https"
	default:
		return "tcp"
	}
}

func (c *Checker) notifyChanges(ctx context.Context, changes []statusChange) {
	if !c.notifier.Configured() || ctx.Err() != nil {
		return
	}
	down := []string{}
	recovered := []string{}
	for _, change := range changes {
		if change.current == StatusDown && change.previous != StatusDown {
			down = append(down, change.name)
		}
		if change.current == StatusUp && change.previous == StatusDown {
			recovered = append(recovered, change.name)
		}
	}
	if len(down)+len(recovered) == 0 {
		return
	}
	var title, body string
	notificationType := notify.TypeFailure
	if len(down)+len(recovered) == 1 {
		var name, key, bodyKey string
		notificationType = notify.TypeSuccess
		if len(down) > 0 {
			name = down[0]
			key, bodyKey = "serviceDown", "serviceDownBody"
			notificationType = notify.TypeFailure
		} else {
			name = recovered[0]
			key, bodyKey = "serviceRecovered", "serviceRecoveredBody"
		}
		title = notify.Message(c.locale, key, map[string]string{"name": name})
		body = notify.Message(c.locale, bodyKey, map[string]string{"name": name})
	} else {
		title = notify.Message(c.locale, "serviceStatusChanges", nil)
		sections := []string{}
		if len(down) > 0 {
			lines := append([]string{notify.Message(c.locale, "servicesDown", map[string]string{"total": strconv.Itoa(len(down))})}, down...)
			for index := 1; index < len(lines); index++ {
				lines[index] = "- " + lines[index]
			}
			sections = append(sections, strings.Join(lines, "\n"))
		}
		if len(recovered) > 0 {
			lines := append([]string{notify.Message(c.locale, "servicesRecovered", map[string]string{"total": strconv.Itoa(len(recovered))})}, recovered...)
			for index := 1; index < len(lines); index++ {
				lines[index] = "- " + lines[index]
			}
			sections = append(sections, strings.Join(lines, "\n"))
		}
		body = strings.Join(sections, "\n\n")
		if len(down) == 0 {
			notificationType = notify.TypeSuccess
		}
	}
	if err := c.notifier.Notify(ctx, title, body, notificationType); err != nil {
		log.Printf("Health check: notification failed: %v", err)
	}
}
