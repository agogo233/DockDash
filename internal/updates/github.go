package updates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"dockdash/internal/registry"
)

const (
	githubAPI       = "https://api.github.com"
	releasePageSize = 100
	maxReleasePages = 50
	githubTimeout   = 8 * time.Second
)

type Release struct {
	Version     string `json:"version"`
	PublishedAt string `json:"publishedAt"`
	Body        string `json:"body"`
	HTMLURL     string `json:"htmlUrl"`
}

type AppUpdate struct {
	HasUpdate bool     `json:"hasUpdate"`
	Release   *Release `json:"release,omitempty"`
}

type Changelog struct {
	Available bool      `json:"available"`
	Reason    string    `json:"reason,omitempty"`
	Release   *Release  `json:"release,omitempty"`
	Releases  []Release `json:"releases,omitempty"`
	complete  bool
}

type githubRelease struct {
	TagName     string `json:"tag_name"`
	PublishedAt string `json:"published_at"`
	Body        string `json:"body"`
	HTMLURL     string `json:"html_url"`
	Draft       bool   `json:"draft"`
}

type GitHub struct {
	http    *http.Client
	baseURL string
	token   string
	mu      sync.Mutex
	app     map[string]cachedApp
	logs    map[string]Changelog
}

type cachedApp struct {
	value     *AppUpdate
	checkedAt time.Time
}

func NewGitHub(token string) *GitHub {
	return &GitHub{
		http:    &http.Client{Timeout: githubTimeout},
		baseURL: githubAPI,
		token:   strings.TrimSpace(token),
		app:     map[string]cachedApp{},
		logs:    map[string]Changelog{},
	}
}

func (g *GitHub) AppUpdate(ctx context.Context, repository, version string, interval time.Duration) *AppUpdate {
	if repository == "" || version == "dev" {
		return nil
	}
	current := registry.ExtractSemVer(version)
	if current == nil {
		return nil
	}
	cacheKey := repository + "\x00" + version
	g.mu.Lock()
	cached, present := g.app[cacheKey]
	if present && time.Since(cached.checkedAt) < interval {
		g.mu.Unlock()
		return cached.value
	}
	g.mu.Unlock()

	release, err := g.requestRelease(ctx, repository+"/releases/latest")
	if err != nil {
		return nil
	}
	latest := registry.ExtractSemVer(release.TagName)
	if latest == nil {
		return nil
	}
	result := &AppUpdate{
		HasUpdate: registry.CompareSemVer(latest, current) > 0,
		Release:   toRelease(release),
	}
	g.mu.Lock()
	g.app[cacheKey] = cachedApp{value: result, checkedAt: time.Now()}
	g.mu.Unlock()
	return result
}

func (g *GitHub) ReleaseURL(ctx context.Context, repository, tag string) string {
	if repository == "" || tag == "" {
		return ""
	}
	release, err := g.fetchRelease(ctx, repository, tag)
	if err != nil {
		return ""
	}
	return release.HTMLURL
}

func (g *GitHub) FetchChangelog(ctx context.Context, repository, currentTag, latestTag string) Changelog {
	if repository == "" {
		return Changelog{Available: false, Reason: "Could not resolve GitHub repository"}
	}
	if latestTag == "" {
		return Changelog{Available: false, Reason: "No image tag available"}
	}
	cacheKey := strings.Join([]string{repository, currentTag, latestTag}, "\x00")
	g.mu.Lock()
	if cached, ok := g.logs[cacheKey]; ok {
		g.mu.Unlock()
		return cached
	}
	g.mu.Unlock()

	result := g.fetchChangelog(ctx, repository, currentTag, latestTag)
	if result.complete {
		g.mu.Lock()
		g.logs[cacheKey] = result
		g.mu.Unlock()
	}
	return result
}

func (g *GitHub) fetchChangelog(ctx context.Context, repository, currentTag, latestTag string) Changelog {
	if currentTag == latestTag {
		release, err := g.fetchRelease(ctx, repository, latestTag)
		if err != nil {
			return Changelog{Available: false, Reason: fmt.Sprintf("No release found for tag \"%s\" in %s", latestTag, repository)}
		}
		converted := toRelease(release)
		return Changelog{Available: true, Release: converted, Releases: []Release{*converted}, complete: true}
	}
	targetCandidates := tagCandidates(latestTag)
	releases := []Release{}
	seen := map[string]bool{}
	complete := false
	current := registry.ExtractSemVer(currentTag)
	latest := registry.ExtractSemVer(latestTag)
	for page := 1; page <= maxReleasePages; page++ {
		if ctx.Err() != nil {
			break
		}
		var pageReleases []githubRelease
		endpoint := repository + "/releases?per_page=" + strconv.Itoa(releasePageSize) + "&page=" + strconv.Itoa(page)
		if err := g.requestJSON(ctx, endpoint, &pageReleases); err != nil {
			break
		}
		for _, item := range pageReleases {
			if item.Draft {
				continue
			}
			parsed := registry.ExtractSemVer(item.TagName)
			if parsed == nil || current == nil || latest == nil {
				continue
			}
			if registry.CompareSemVer(parsed, current) <= 0 {
				continue
			}
			comparison := registry.CompareSemVer(parsed, latest)
			if comparison > 0 || (comparison == 0 && !targetCandidates[item.TagName]) {
				continue
			}
			key := strings.Join(intStrings(parsed.Parts), ".")
			if seen[key] {
				continue
			}
			seen[key] = true
			releases = append(releases, toRelease(&item))
		}
		if len(pageReleases) < releasePageSize {
			complete = true
			break
		}
	}
	if len(releases) == 0 {
		if release, err := g.fetchRelease(ctx, repository, latestTag); err == nil {
			releases = []Release{*toRelease(release)}
		}
	}
	if len(releases) == 0 {
		return Changelog{Available: false, Reason: fmt.Sprintf("No release found for tag \"%s\" in %s", latestTag, repository)}
	}
	sort.SliceStable(releases, func(i, j int) bool {
		return registry.CompareSemVer(registry.ExtractSemVer(releases[i].Version),
			registry.ExtractSemVer(releases[j].Version)) > 0
	})
	result := Changelog{Available: true, Releases: releases, complete: complete}
	for _, release := range releases {
		if targetCandidates[release.Version] {
			target := release
			result.Release = &target
			break
		}
	}
	return result
}

func (g *GitHub) fetchRelease(ctx context.Context, repository, tag string) (*githubRelease, error) {
	for _, candidate := range candidateList(tag) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		release, err := g.requestRelease(ctx, repository+"/releases/tags/"+candidate)
		if err == nil {
			return release, nil
		}
	}
	return nil, errors.New("release not found")
}

func (g *GitHub) requestRelease(ctx context.Context, endpoint string) (*githubRelease, error) {
	var release githubRelease
	if err := g.requestJSON(ctx, endpoint, &release); err != nil {
		return nil, err
	}
	return &release, nil
}

func (g *GitHub) requestJSON(ctx context.Context, endpoint string, target any) error {
	normalized, err := normalizeEndpoint(endpoint)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, g.baseURL+"/repos/"+normalized, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	if g.token != "" {
		request.Header.Set("Authorization", "Bearer "+g.token)
	}
	response, err := g.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<16))
		return fmt.Errorf("GitHub API HTTP %d", response.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(target)
}

func normalizeEndpoint(endpoint string) (string, error) {
	parts := strings.SplitN(endpoint, "?", 2)
	segments := strings.Split(parts[0], "/")
	if len(segments) < 2 || !repositorySegment(segments[0]) || !repositorySegment(stripTag(segments[1])) {
		return "", errors.New("invalid GitHub repository")
	}
	for index := 1; index < len(segments); index++ {
		segments[index] = url.PathEscape(segments[index])
	}
	result := strings.Join(segments, "/")
	if len(parts) == 2 {
		result += "?" + parts[1]
	}
	return result, nil
}

func toRelease(item *githubRelease) *Release {
	return &Release{Version: item.TagName, PublishedAt: item.PublishedAt, Body: item.Body, HTMLURL: item.HTMLURL}
}

func candidateList(tag string) []string {
	seen := map[string]bool{}
	order := []string{}
	add := func(candidate string) {
		if candidate == "" || seen[candidate] {
			return
		}
		seen[candidate] = true
		order = append(order, candidate)
	}
	addWithVariant := func(candidate string) {
		add(candidate)
		if strings.HasPrefix(candidate, "v") {
			add(strings.TrimPrefix(candidate, "v"))
		} else {
			add("v" + candidate)
		}
	}
	addWithVariant(tag)
	parsed := registry.ExtractSemVer(tag)
	if parsed == nil {
		return order
	}
	if parsed.Prefix != "" || parsed.Suffix != "" {
		addWithVariant(parsed.Version)
	}
	parts := append([]int{}, parsed.Parts...)
	for len(parts) > 2 && parts[len(parts)-1] == 0 {
		parts = parts[:len(parts)-1]
		addWithVariant(parsed.Prefix + strings.Join(intStrings(parts), ".") + parsed.Suffix)
	}
	return order
}

func tagCandidates(tag string) map[string]bool {
	result := map[string]bool{}
	for _, candidate := range candidateList(tag) {
		result[candidate] = true
	}
	return result
}

func intStrings(values []int) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, strconv.Itoa(value))
	}
	return result
}

func ResolveRepository(image string) string {
	image = strings.TrimPrefix(image, "docker.io/")
	image = strings.TrimPrefix(image, "index.docker.io/")
	if strings.HasPrefix(image, "ghcr.io/") {
		parts := strings.Split(image, "/")
		if len(parts) >= 3 {
			return validRepository(parts[1], stripTag(parts[2]))
		}
		return ""
	}
	parts := strings.Split(image, "/")
	if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
		return validRepository(parts[0], stripTag(parts[1]))
	}
	return ""
}

func stripTag(name string) string {
	if index := strings.Index(name, "@"); index >= 0 {
		name = name[:index]
	}
	if index := strings.LastIndex(name, ":"); index >= 0 {
		name = name[:index]
	}
	return name
}

func validRepository(owner, name string) string {
	if repositorySegment(owner) && repositorySegment(name) &&
		owner != "." && owner != ".." && name != "." && name != ".." {
		return owner + "/" + name
	}
	return ""
}

func repositorySegment(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '_' || char == '-' || char == '.' {
			continue
		}
		return false
	}
	return true
}
