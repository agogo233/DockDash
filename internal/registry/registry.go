package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	requestTimeout = 8 * time.Second
	maxPages       = 50
	maxRedirects   = 5
	dockerHubURL   = "registry-1.docker.io"
	dockerHubAPI   = "https://hub.docker.com/v2/repositories"
	ghcrURL        = "ghcr.io"
	manifestAccept = "application/vnd.oci.image.index.v1+json,application/vnd.docker.distribution.manifest.list.v2+json,application/vnd.docker.distribution.manifest.v2+json,application/vnd.oci.image.manifest.v1+json"
)

type ImageRef struct {
	Registry   string
	Repository string
	Tag        string
}

type TagScanResult struct {
	Tags     []string
	Complete bool
	Failed   bool
}

type parsedPage struct {
	Tags []string
	Next string
}

var bearerPattern = regexp.MustCompile(`(?i)Bearer\s+(.+)`)
var authParamPattern = regexp.MustCompile(`(\w+)="([^"]+)"`)

type Client struct {
	http        *http.Client
	githubToken string
}

func New(githubToken string) *Client {
	return &Client{
		http: &http.Client{
			Timeout: requestTimeout,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		githubToken: strings.TrimSpace(githubToken),
	}
}

func registryBase(registry string) string {
	if strings.HasPrefix(registry, "http://") || strings.HasPrefix(registry, "https://") {
		return strings.TrimRight(registry, "/")
	}
	return "https://" + registry
}

func (c *Client) ParseImageRef(image string) ImageRef {
	withoutDigest := strings.Split(image, "@")[0]
	segments := strings.Split(withoutDigest, "/")
	last := segments[len(segments)-1]
	tag := "latest"
	if index := strings.LastIndex(last, ":"); index >= 0 {
		segments[len(segments)-1] = last[:index]
		tag = last[index+1:]
	}
	nameWithoutTag := strings.Join(segments, "/")
	first := segments[0]
	if !strings.Contains(first, ".") && !strings.Contains(first, ":") {
		repository := nameWithoutTag
		if len(segments) == 1 {
			repository = "library/" + nameWithoutTag
		}
		return ImageRef{Registry: dockerHubURL, Repository: repository, Tag: tag}
	}
	return ImageRef{Registry: first, Repository: strings.Join(segments[1:], "/"), Tag: tag}
}

func (c *Client) request(ctx context.Context, method, target string, headers map[string]string, body io.Reader) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, err
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	return c.http.Do(request)
}

func (c *Client) token(ctx context.Context, base, repository string) string {
	response, err := c.request(ctx, http.MethodGet, base+"/v2/", nil, nil)
	if err != nil {
		return ""
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusOK {
		return ""
	}
	if response.StatusCode != http.StatusUnauthorized {
		return ""
	}
	challenge := response.Header.Get("WWW-Authenticate")
	match := bearerPattern.FindStringSubmatch(challenge)
	if match == nil {
		return ""
	}
	realm := ""
	values := url.Values{}
	for _, pair := range authParamPattern.FindAllStringSubmatch(match[1], -1) {
		if pair[1] == "realm" {
			realm = pair[2]
		} else {
			values.Set(pair[1], pair[2])
		}
	}
	if realm == "" {
		return ""
	}
	parsedRealm, err := url.Parse(realm)
	if err != nil || (parsedRealm.Scheme != "http" && parsedRealm.Scheme != "https") {
		return ""
	}
	values.Set("scope", "repository:"+repository+":pull")
	separator := "?"
	if strings.Contains(realm, "?") {
		separator = "&"
	}
	tokenResponse, err := c.request(ctx, http.MethodGet, realm+separator+values.Encode(), nil, nil)
	if err != nil {
		return ""
	}
	defer tokenResponse.Body.Close()
	var payload struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(io.LimitReader(tokenResponse.Body, 1024*1024)).Decode(&payload); err != nil {
		return ""
	}
	if payload.Token != "" {
		return payload.Token
	}
	return payload.AccessToken
}

func validCursor(candidate, current string) (string, error) {
	parsed, err := url.Parse(candidate)
	if err != nil {
		return "", errors.New("invalid pagination URL")
	}
	base, err := url.Parse(current)
	if err != nil {
		return "", errors.New("invalid pagination URL")
	}
	resolved := base.ResolveReference(parsed)
	if resolved.Scheme != base.Scheme || resolved.Host != base.Host || resolved.Path != base.Path ||
		resolved.User != nil || resolved.Fragment != "" {
		return "", errors.New("unsafe pagination URL")
	}
	return resolved.String(), nil
}

func isRedirect(status int) bool {
	return status == 301 || status == 302 || status == 303 || status == 307 || status == 308
}

func (c *Client) scanTagPages(ctx context.Context, first string, headers map[string]string,
	parse func([]byte, http.Header, *url.URL) (parsedPage, error), runningTag string) TagScanResult {
	var err error
	if _, err = url.Parse(first); err != nil {
		return TagScanResult{Failed: true}
	}
	cursor := first
	seen := map[string]bool{}
	tags := map[string]bool{}
	for page := 0; page < maxPages; page++ {
		var response *http.Response
		for redirects := 0; ; redirects++ {
			if err := ctx.Err(); err != nil {
				return TagScanResult{Failed: true}
			}
			if seen[cursor] {
				return TagScanResult{Failed: true}
			}
			seen[cursor] = true
			response, err = c.request(ctx, http.MethodGet, cursor, headers, nil)
			if err != nil {
				return TagScanResult{Failed: true}
			}
			if !isRedirect(response.StatusCode) {
				break
			}
			if redirects >= maxRedirects {
				response.Body.Close()
				return TagScanResult{Failed: true}
			}
			location := response.Header.Get("Location")
			response.Body.Close()
			if strings.TrimSpace(location) == "" {
				return TagScanResult{Failed: true}
			}
			cursor, err = validCursor(location, cursor)
			if err != nil {
				return TagScanResult{Failed: true}
			}
		}
		body, err := io.ReadAll(io.LimitReader(response.Body, 8*1024*1024))
		response.Body.Close()
		if err != nil || response.StatusCode != http.StatusOK {
			return TagScanResult{Failed: true}
		}
		currentURL, err := url.Parse(cursor)
		if err != nil {
			return TagScanResult{Failed: true}
		}
		result, err := parse(body, response.Header, currentURL)
		if err != nil {
			return TagScanResult{Failed: true}
		}
		for _, tag := range result.Tags {
			tags[tag] = true
		}
		if result.Next == "" {
			return TagScanResult{Tags: tagSetToSlice(tags), Complete: true}
		}
		if runningTag != "" && ContainsUpdate(result.Tags, runningTag) {
			return TagScanResult{Tags: tagSetToSlice(tags), Complete: false}
		}
		next, err := validCursor(result.Next, cursor)
		if err != nil {
			return TagScanResult{Failed: true}
		}
		cursor = next
	}
	return TagScanResult{Tags: tagSetToSlice(tags), Complete: false}
}

func tagSetToSlice(tags map[string]bool) []string {
	result := make([]string, 0, len(tags))
	for tag := range tags {
		result = append(result, tag)
	}
	return result
}

func (c *Client) scanDockerHub(ctx context.Context, ref ImageRef, prefix, runningTag string) (TagScanResult, error) {
	nameParam := ""
	if prefix != "" && prefix != "v" {
		nameParam = "&name=" + url.QueryEscape(prefix)
	}
	first := dockerHubAPI + "/" + ref.Repository + "/tags?page_size=100&ordering=last_updated" + nameParam
	return c.scanTagPages(ctx, first, nil, func(data []byte, _ http.Header, location *url.URL) (parsedPage, error) {
		var payload struct {
			Count   *int64 `json:"count"`
			Results []struct {
				Name *string `json:"name"`
			} `json:"results"`
		}
		if err := json.Unmarshal(data, &payload); err != nil || payload.Count == nil ||
			*payload.Count < 0 || payload.Results == nil {
			return parsedPage{}, errors.New("invalid response")
		}
		pageNumber := 1
		if raw := location.Query().Get("page"); raw != "" {
			parsed, err := parsePositive(raw)
			if err != nil {
				return parsedPage{}, errors.New("invalid page")
			}
			pageNumber = parsed
		}
		tags := make([]string, 0, len(payload.Results))
		for _, entry := range payload.Results {
			if entry.Name == nil {
				return parsedPage{}, errors.New("invalid response")
			}
			tags = append(tags, *entry.Name)
		}
		next := ""
		if int64(pageNumber)*100 < *payload.Count {
			copyURL := *location
			values := copyURL.Query()
			values.Set("page", fmt.Sprintf("%d", pageNumber+1))
			copyURL.RawQuery = values.Encode()
			next = copyURL.String()
		}
		return parsedPage{Tags: tags, Next: next}, nil
	}, runningTag), nil
}

func parsePositive(value string) (int, error) {
	result := 0
	for _, char := range value {
		if char < '0' || char > '9' {
			return 0, errors.New("not a number")
		}
		result = result*10 + int(char-'0')
	}
	return result, nil
}

var linkPattern = regexp.MustCompile(`(?i)<([^<>]+)>\s*;\s*([^,]*)`)
var nextRelPattern = regexp.MustCompile(`(?i)(?:^|[;\s])rel\s*=\s*"?(?:[^";\s]+\s+)*next(?:\s+[^";\s]+)*"?`)
var relParamPattern = regexp.MustCompile(`(?i)(?:^|;)\s*rel\s*=`)

func nextLink(header string) (string, error) {
	if strings.TrimSpace(header) == "" {
		return "", nil
	}
	matches := linkPattern.FindAllStringSubmatch(header, -1)
	if len(matches) == 0 {
		return "", errors.New("invalid link header")
	}
	next := ""
	for _, match := range matches {
		if !relParamPattern.MatchString(match[2]) {
			return "", errors.New("invalid link header")
		}
		if nextRelPattern.MatchString(match[2]) {
			if next != "" && next != match[1] {
				return "", errors.New("invalid link header")
			}
			next = match[1]
		}
	}
	return next, nil
}

func (c *Client) scanGeneric(ctx context.Context, ref ImageRef, runningTag string, headers map[string]string) (TagScanResult, error) {
	base := registryBase(ref.Registry)
	if headers == nil {
		headers = map[string]string{}
	}
	token := c.token(ctx, base, ref.Repository)
	requestHeaders := map[string]string{}
	for key, value := range headers {
		requestHeaders[key] = value
	}
	if token != "" {
		requestHeaders["Authorization"] = "Bearer " + token
	}
	return c.scanTagPages(ctx, base+"/v2/"+ref.Repository+"/tags/list?n=1000", requestHeaders,
		func(data []byte, responseHeaders http.Header, _ *url.URL) (parsedPage, error) {
			var payload struct {
				Tags []string `json:"tags"`
			}
			if err := json.Unmarshal(data, &payload); err != nil {
				return parsedPage{}, errors.New("invalid response")
			}
			next, err := nextLink(responseHeaders.Get("Link"))
			if err != nil {
				return parsedPage{}, err
			}
			return parsedPage{Tags: payload.Tags, Next: next}, nil
		}, runningTag), nil
}

func (c *Client) scanGHCR(ctx context.Context, ref ImageRef, prefix, runningTag string) (TagScanResult, error) {
	if c.githubToken == "" {
		return c.scanGeneric(ctx, ref, "", nil)
	}
	parts := strings.Split(ref.Repository, "/")
	owner := parts[0]
	packageName := url.QueryEscape(strings.Join(parts[1:], "/"))
	headers := map[string]string{
		"Authorization":         "Bearer " + c.githubToken,
		"Accept":                "application/vnd.github+json",
		"X-GitHub-Api-Version":  "2022-11-28",
	}
	if err := ctx.Err(); err != nil {
		return TagScanResult{Failed: true}, nil
	}
	for _, ownerType := range []string{"orgs", "users"} {
		first := "https://api.github.com/" + ownerType + "/" + owner + "/packages/container/" +
			packageName + "/versions?per_page=100&page=1"
		result := c.scanTagPages(ctx, first, headers, func(data []byte, _ http.Header, location *url.URL) (parsedPage, error) {
			var versions []struct {
				Metadata struct {
					Container struct {
						Tags []string `json:"tags"`
					} `json:"container"`
				} `json:"metadata"`
			}
			if err := json.Unmarshal(data, &versions); err != nil {
				return parsedPage{}, errors.New("invalid response")
			}
			tags := []string{}
			for _, version := range versions {
				tags = append(tags, version.Metadata.Container.Tags...)
			}
			next := ""
			if len(versions) == 100 {
				page := 1
				if raw := location.Query().Get("page"); raw != "" {
					if parsed, err := parsePositive(raw); err == nil {
						page = parsed
					}
				}
				copyURL := *location
				values := copyURL.Query()
				values.Set("page", fmt.Sprintf("%d", page+1))
				copyURL.RawQuery = values.Encode()
				next = copyURL.String()
			}
			return parsedPage{Tags: tags, Next: next}, nil
		}, runningTag)
		if !result.Failed {
			return result, nil
		}
	}
	if err := ctx.Err(); err != nil {
		return TagScanResult{Failed: true}, nil
	}
	return c.scanGeneric(ctx, ref, "", nil)
}

func (c *Client) ScanRepositoryTags(ctx context.Context, ref ImageRef, prefix, runningTag string) TagScanResult {
	if runningTag == "" {
		runningTag = ref.Tag
	}
	var result TagScanResult
	var err error
	switch ref.Registry {
	case dockerHubURL:
		result, err = c.scanDockerHub(ctx, ref, prefix, runningTag)
	case ghcrURL:
		result, err = c.scanGHCR(ctx, ref, prefix, runningTag)
	default:
		result, err = c.scanGeneric(ctx, ref, "", nil)
	}
	if err != nil || result.Failed {
		return TagScanResult{Failed: true}
	}
	return result
}

func (c *Client) GetRepositoryTags(ctx context.Context, ref ImageRef, prefix string) []string {
	result := c.ScanRepositoryTags(ctx, ref, prefix, "")
	if result.Failed || (!result.Complete && !ContainsUpdate(result.Tags, ref.Tag)) {
		return nil
	}
	return result.Tags
}

func (c *Client) GetManifestDigest(ctx context.Context, ref ImageRef) string {
	base := registryBase(ref.Registry)
	token := c.token(ctx, base, ref.Repository)
	headers := map[string]string{"Accept": manifestAccept}
	if token != "" {
		headers["Authorization"] = "Bearer " + token
	}
	target := base + "/v2/" + ref.Repository + "/manifests/" + ref.Tag
	for _, method := range []string{http.MethodHead, http.MethodGet} {
		response, err := c.request(ctx, method, target, headers, nil)
		if err != nil {
			continue
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1*1024*1024))
		response.Body.Close()
		if response.StatusCode == http.StatusOK {
			return response.Header.Get("Docker-Content-Digest")
		}
	}
	return ""
}
