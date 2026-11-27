package monitor

import (
	"context"
	"log"
	"strconv"
	"sync"
	"time"

	"dockdash/internal/config"
	"dockdash/internal/docker"
	"dockdash/internal/notify"
	"dockdash/internal/repo"
)

type StatsReader interface {
	Stats(ctx context.Context, service repo.Service) (docker.ContainerStats, error)
}

type Sample struct {
	CPUPercent    float64 `json:"cpuPercent"`
	MemoryPercent float64 `json:"memoryPercent"`
}

type Monitor struct {
	repo          *repo.Repo
	docker        StatsReader
	notifier      *notify.Client
	locale        string
	enabled       bool
	history       bool
	cpuThreshold  int
	memThreshold  int
	spikeDuration int
	mu            sync.Mutex
	latest        map[string]Sample
	cpuSpiking    map[string]bool
	memSpiking    map[string]bool
	cpuStart      map[string]time.Time
}

func New(database *repo.Repo, reader StatsReader, notifier *notify.Client, cfg *config.Config) *Monitor {
	return &Monitor{
		repo: database, docker: reader, notifier: notifier, locale: cfg.Locale,
		enabled: cfg.ResourceMonitorEnabled, history: cfg.HealthHistoryEnabled,
		cpuThreshold: cfg.CPUSpikeThreshold, memThreshold: cfg.MemorySpikeThreshold,
		spikeDuration: cfg.SpikeDurationThreshold,
		latest:        map[string]Sample{}, cpuSpiking: map[string]bool{},
		memSpiking: map[string]bool{}, cpuStart: map[string]time.Time{},
	}
}

func (m *Monitor) Latest(serviceID string) (Sample, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sample, ok := m.latest[serviceID]
	return sample, ok
}

func (m *Monitor) Collect(ctx context.Context) error {
	if !m.enabled {
		return nil
	}
	services, err := m.repo.GetServices()
	if err != nil {
		return err
	}
	semaphore := make(chan struct{}, 10)
	var waitGroup sync.WaitGroup
	for _, service := range services {
		if service.Source != "docker" || ctx.Err() != nil {
			continue
		}
		waitGroup.Add(1)
		semaphore <- struct{}{}
		go func(service repo.Service) {
			defer waitGroup.Done()
			defer func() { <-semaphore }()
			stats, statsErr := m.docker.Stats(ctx, service)
			if statsErr != nil || ctx.Err() != nil {
				return
			}
			sample := Sample{CPUPercent: stats.CPUPercent, MemoryPercent: stats.MemoryPercent}
			m.mu.Lock()
			m.latest[service.ID] = sample
			m.mu.Unlock()
			if m.history {
				if err := m.repo.AddResourceHistory(service.ID, sample.CPUPercent, sample.MemoryPercent); err != nil {
					log.Printf("Resource monitor: failed to record history for %q: %v", service.Name, err)
				}
			}
			m.checkSpikes(ctx, service, sample)
		}(service)
	}
	waitGroup.Wait()
	return nil
}

func (m *Monitor) checkSpikes(ctx context.Context, service repo.Service, sample Sample) {
	if !m.notifier.Configured() || ctx.Err() != nil {
		return
	}
	type pendingNotice struct {
		titleKey string
		bodyKey  string
		kind     notify.NotificationType
		values   map[string]string
	}
	notices := []pendingNotice{}
	m.mu.Lock()
	if m.cpuThreshold > 0 {
		spiking := m.cpuSpiking[service.ID]
		if sample.CPUPercent >= float64(m.cpuThreshold) {
			if m.cpuStart[service.ID].IsZero() {
				m.cpuStart[service.ID] = time.Now()
			}
			if !spiking && time.Since(m.cpuStart[service.ID]) >= time.Duration(m.spikeDuration)*time.Second {
				m.cpuSpiking[service.ID] = true
				notices = append(notices, pendingNotice{"cpuSpike", "cpuSpikeBody", notify.TypeWarning, map[string]string{"name": service.Name, "percent": formatPercent(sample.CPUPercent)}})
			}
		} else {
			if spiking {
				m.cpuSpiking[service.ID] = false
				notices = append(notices, pendingNotice{"cpuRecovered", "cpuRecoveredBody", notify.TypeSuccess, map[string]string{"name": service.Name}})
			}
			delete(m.cpuStart, service.ID)
		}
	}
	if m.memThreshold > 0 {
		spiking := m.memSpiking[service.ID]
		now := sample.MemoryPercent >= float64(m.memThreshold)
		if now && !spiking {
			m.memSpiking[service.ID] = true
			notices = append(notices, pendingNotice{"memorySpike", "memorySpikeBody", notify.TypeWarning, map[string]string{"name": service.Name, "percent": formatPercent(sample.MemoryPercent)}})
		} else if !now && spiking {
			m.memSpiking[service.ID] = false
			notices = append(notices, pendingNotice{"memoryRecovered", "memoryRecoveredBody", notify.TypeSuccess, map[string]string{"name": service.Name}})
		}
	}
	m.mu.Unlock()
	for _, notice := range notices {
		m.notify(ctx, notice.titleKey, notice.bodyKey, notice.kind, notice.values)
	}
}

func formatPercent(value float64) string {
	return strconv.FormatFloat(value, 'f', 1, 64)
}

func (m *Monitor) notify(ctx context.Context, titleKey, bodyKey string, notificationType notify.NotificationType, values map[string]string) {
	title := notify.Message(m.locale, titleKey, values)
	body := notify.Message(m.locale, bodyKey, values)
	if err := m.notifier.Notify(ctx, title, body, notificationType); err != nil {
		log.Printf("Resource monitor: notification failed: %v", err)
	}
}
