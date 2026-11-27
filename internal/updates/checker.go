package updates

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	"dockdash/internal/notify"
	"dockdash/internal/registry"
	"dockdash/internal/repo"
)

const concurrencyLimit = 5

type Registry interface {
	ParseImageRef(image string) registry.ImageRef
	GetRepositoryTags(ctx context.Context, ref registry.ImageRef, prefix string) []string
	GetManifestDigest(ctx context.Context, ref registry.ImageRef) string
}

type Notifier interface {
	Configured() bool
	Notify(ctx context.Context, title, body string, notificationType notify.NotificationType) error
}

type ReleaseResolver interface {
	ReleaseURL(ctx context.Context, image, tag string) string
}

type Checker struct {
	repo       *repo.Repo
	registry   Registry
	notifier   Notifier
	releases   ReleaseResolver
	locale     string
	concurrent int
}

type checkResult struct {
	service        repo.Service
	hasUpdate      bool
	previousUpdate bool
	currentVersion string
	latestVersion  string
	releaseURL     string
	snapshotImage  string
	snapshotTag    string
	snapshotDigest string
}

type updateNotice struct {
	name           string
	currentVersion string
	latestVersion  string
	releaseURL     string
}

func NewChecker(database *repo.Repo, reg Registry, notifier Notifier, releases ReleaseResolver, locale string) *Checker {
	return &Checker{repo: database, registry: reg, notifier: notifier, releases: releases, locale: locale, concurrent: concurrencyLimit}
}

func (c *Checker) CheckAll(ctx context.Context) error {
	services, err := c.repo.GetServices()
	if err != nil {
		return err
	}
	candidates := make([]repo.Service, 0, len(services))
	for _, service := range services {
		if service.Source != "docker" || ctx.Err() != nil {
			continue
		}
		if _, ok := service.Metadata["image"].(string); !ok {
			continue
		}
		if _, ok := service.Metadata["imageTag"].(string); !ok {
			continue
		}
		candidates = append(candidates, service)
	}
	results := c.checkConcurrent(ctx, candidates)
	notices := []updateNotice{}
	for _, result := range results {
		patch := map[string]any{
			"hasUpdate":       result.hasUpdate,
			"updateCheckedAt": time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		}
		if result.latestVersion != "" {
			patch["latestVersion"] = result.latestVersion
		}
		updated, err := c.repo.UpdateServiceMetadataIfUnchanged(result.service.ID,
			result.snapshotImage, result.snapshotTag, result.snapshotDigest, patch)
		if err != nil {
			log.Printf("Update check: failed to save %q: %v", result.service.Name, err)
			continue
		}
		if !updated || !result.hasUpdate || result.previousUpdate ||
			result.currentVersion == "" || result.latestVersion == "" {
			continue
		}
		notices = append(notices, updateNotice{
			name: result.service.Name, currentVersion: result.currentVersion,
			latestVersion: result.latestVersion, releaseURL: result.releaseURL,
		})
	}
	if len(notices) == 0 || ctx.Err() != nil {
		return nil
	}
	titleKey := "updateAvailable"
	if len(notices) > 1 {
		titleKey = "updatesAvailable"
	}
	title := notify.Message(c.locale, titleKey, nil)
	entries := make([]string, 0, len(notices))
	for _, notice := range notices {
		entry := notify.Message(c.locale, "updateEntry", map[string]string{
			"name": notice.name, "currentVersion": notice.currentVersion, "latestVersion": notice.latestVersion,
		})
		if notice.releaseURL != "" {
			entry += "\n  " + notice.releaseURL
		}
		entries = append(entries, "• "+entry)
	}
	if !c.notificationConfigured() {
		return nil
	}
	if err := c.notifier.Notify(ctx, title, strings.Join(entries, "\n"), notify.TypeWarning); err != nil {
		log.Printf("Update check: notification failed: %v", err)
	}
	return nil
}

func (c *Checker) checkConcurrent(ctx context.Context, services []repo.Service) []*checkResult {
	results := make([]*checkResult, len(services))
	semaphore := make(chan struct{}, c.concurrent)
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
			result, ok := c.checkService(ctx, service)
			if ok {
				results[index] = result
			}
		}(index, service)
	}
	waitGroup.Wait()
	ordered := make([]*checkResult, 0, len(results))
	for _, result := range results {
		if result != nil {
			ordered = append(ordered, result)
		}
	}
	return ordered
}

func (c *Checker) checkService(ctx context.Context, service repo.Service) (*checkResult, bool) {
	image, _ := service.Metadata["image"].(string)
	imageTag, _ := service.Metadata["imageTag"].(string)
	imageDigest, _ := service.Metadata["imageDigest"].(string)
	snapshot := &checkResult{
		service: service, previousUpdate: metadataBool(service.Metadata, "hasUpdate"),
		snapshotImage: image, snapshotTag: imageTag, snapshotDigest: imageDigest,
	}
	if image == "" || imageTag == "" || ctx.Err() != nil {
		return nil, false
	}
	ref := c.registry.ParseImageRef(image + ":" + imageTag)
	parsed := registry.ExtractSemVer(imageTag)
	if imageTag != "latest" && parsed != nil {
		tags := c.registry.GetRepositoryTags(ctx, ref, parsed.Prefix)
		if len(tags) == 0 || ctx.Err() != nil {
			return nil, false
		}
		highest := parsed
		highestTag := ""
		for _, tag := range tags {
			candidate := registry.ExtractSemVer(tag)
			if !registry.SameFamily(candidate, parsed) {
				continue
			}
			comparison := registry.CompareSemVer(candidate, highest)
			if comparison > 0 || (comparison == 0 && len(candidate.Parts) > len(highest.Parts)) {
				highest = candidate
				highestTag = tag
			}
		}
		if highestTag == "" || registry.CompareSemVer(highest, parsed) <= 0 {
			snapshot.hasUpdate = false
			return snapshot, true
		}
		snapshot.hasUpdate = true
		snapshot.currentVersion = imageTag
		snapshot.latestVersion = highestTag
	} else {
		remoteDigest := c.registry.GetManifestDigest(ctx, ref)
		if imageDigest == "" || remoteDigest == "" || ctx.Err() != nil {
			return nil, false
		}
		snapshot.hasUpdate = imageDigest != remoteDigest
		if snapshot.hasUpdate {
			snapshot.currentVersion = shortDigest(imageDigest)
			snapshot.latestVersion = shortDigest(remoteDigest)
		}
	}
	if snapshot.hasUpdate && !snapshot.previousUpdate && snapshot.currentVersion != "" &&
		c.notificationConfigured() && parsed != nil && ctx.Err() == nil && c.releases != nil {
		snapshot.releaseURL = c.releases.ReleaseURL(ctx, ResolveRepository(image), snapshot.latestVersion)
	}
	return snapshot, true
}

func (c *Checker) notificationConfigured() bool {
	return c.notifier != nil && c.notifier.Configured()
}

func metadataBool(metadata map[string]any, key string) bool {
	value, _ := metadata[key].(bool)
	return value
}

func shortDigest(digest string) string {
	if len(digest) > 19 {
		return digest[7:19] + "…"
	}
	return digest
}
