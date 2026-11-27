package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"dockdash/internal/config"
)

const requestTimeout = 5 * time.Second

var retryDelays = []time.Duration{time.Second, 3 * time.Second}

type NotificationType string

const (
	TypeInfo    NotificationType = "info"
	TypeSuccess NotificationType = "success"
	TypeWarning NotificationType = "warning"
	TypeFailure NotificationType = "failure"
)

type Client struct {
	http   *http.Client
	url    string
	urls   []string
	tags   []string
	locale string
}

func New(cfg *config.Config) *Client {
	return newClient(cfg, &http.Client{Timeout: requestTimeout})
}

func newClient(cfg *config.Config, httpClient *http.Client) *Client {
	return &Client{
		http:   httpClient,
		url:    strings.TrimSpace(cfg.AppriseURL),
		urls:   append([]string{}, cfg.AppriseURLs...),
		tags:   append([]string{}, cfg.AppriseTags...),
		locale: cfg.Locale,
	}
}

func (c *Client) Configured() bool { return c.url != "" }

func (c *Client) Notify(ctx context.Context, title, body string, notificationType NotificationType) error {
	if !c.Configured() {
		return nil
	}
	payload := map[string]any{
		"title": "DockDash — " + title,
		"body":  body,
		"type":  string(notificationType),
	}
	if len(c.urls) > 0 {
		payload["urls"] = c.urls
	}
	if len(c.tags) > 0 {
		payload["tag"] = c.tags
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	for attempt := 0; ; attempt++ {
		status, err := c.post(ctx, data)
		message := "network error"
		if err != nil {
			message = err.Error()
		} else if status >= http.StatusOK && status < http.StatusBadRequest {
			return nil
		} else {
			err = fmt.Errorf("HTTP %d", status)
			message = err.Error()
		}
		if !retryable(status, err) || attempt >= len(retryDelays) {
			return fmt.Errorf("Apprise notification failed: %s", message)
		}
		log.Printf("Apprise notification attempt %d failed; retrying: %s", attempt+1, message)
		delay := retryDelays[attempt]
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (c *Client) post(ctx context.Context, data []byte) (int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(data))
	if err != nil {
		return 0, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
	return response.StatusCode, nil
}

func retryable(status int, err error) bool {
	if status == 0 {
		return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
	}
	return status == 408 || status == 424 || status == 429 || status >= 500
}
