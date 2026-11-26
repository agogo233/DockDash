package server

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"dockdash/internal/config"
	"dockdash/internal/session"
)

type contextKey int

const sessionKey contextKey = 0

func sessionFrom(r *http.Request) *session.Session {
	value, _ := r.Context().Value(sessionKey).(*session.Session)
	return value
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeError(writer http.ResponseWriter, status int, message string) {
	writeJSON(writer, status, map[string]string{"error": message})
}

func withBodyLimit(limit int64, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		request.Body = http.MaxBytesReader(writer, request.Body, limit)
		next.ServeHTTP(writer, request)
	})
}

func withSession(store *session.Store, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		sess := store.Get(request)
		if sess != nil {
			request = request.WithContext(context.WithValue(request.Context(), sessionKey, sess))
		}
		next.ServeHTTP(writer, request)
	})
}

type limiter struct {
	mu        sync.Mutex
	window    time.Duration
	limit     int
	hits      map[string][]time.Time
	lastSweep time.Time
}

func newLimiter(limit int, window time.Duration) *limiter {
	return &limiter{window: window, limit: limit, hits: map[string][]time.Time{}, lastSweep: time.Now()}
}

func (l *limiter) allow(key string) (bool, int, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	cutoff := now.Add(-l.window)
	if now.Sub(l.lastSweep) > l.window {
		for candidate, hits := range l.hits {
			kept := hits[:0]
			for _, hit := range hits {
				if hit.After(cutoff) {
					kept = append(kept, hit)
				}
			}
			if len(kept) == 0 {
				delete(l.hits, candidate)
			} else {
				l.hits[candidate] = kept
			}
		}
		l.lastSweep = now
	}
	recent := l.hits[key][:0]
	for _, hit := range l.hits[key] {
		if hit.After(cutoff) {
			recent = append(recent, hit)
		}
	}
	l.hits[key] = recent
	remaining := l.limit - len(recent)
	if remaining <= 0 {
		return false, 0, int(l.window.Seconds())
	}
	l.hits[key] = append(recent, now)
	return true, remaining - 1, int(l.window.Seconds())
}

func withRateLimit(l *limiter, cfg *config.Config, next http.Handler) http.Handler {
	return withRateLimitMessage(l, cfg, next, "Too many API requests, please try again later")
}

func withRateLimitMessage(l *limiter, cfg *config.Config, next http.Handler, message string) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		allowed, remaining, reset := l.allow(clientIP(cfg, request))
		writer.Header().Set("RateLimit", "limit="+strconv.Itoa(l.limit)+", remaining="+strconv.Itoa(remaining)+", reset="+strconv.Itoa(reset))
		writer.Header().Set("RateLimit-Policy", strconv.Itoa(l.limit)+";w="+strconv.Itoa(reset))
		if !allowed {
			writeError(writer, http.StatusTooManyRequests, message)
			return
		}
		next.ServeHTTP(writer, request)
	})
}

func trustedProxy(cfg *config.Config, request *http.Request) bool {
	value := strings.TrimSpace(cfg.TrustProxy)
	if value == "" || value == "false" {
		return false
	}
	if value == "true" {
		return true
	}
	ip := net.ParseIP(remoteHost(request))
	if ip == nil {
		return false
	}
	for _, token := range strings.Split(value, ",") {
		switch strings.TrimSpace(token) {
		case "loopback":
			if ip.IsLoopback() {
				return true
			}
		case "uniquelocal":
			if ip.IsPrivate() || ip.IsLinkLocalUnicast() {
				return true
			}
		default:
			if parsed := net.ParseIP(strings.TrimSpace(token)); parsed != nil && parsed.Equal(ip) {
				return true
			}
		}
	}
	return false
}

func clientIP(cfg *config.Config, request *http.Request) string {
	if trustedProxy(cfg, request) {
		forwarded := request.Header.Get("X-Forwarded-For")
		if forwarded != "" {
			if index := strings.Index(forwarded, ","); index >= 0 {
				return strings.TrimSpace(forwarded[:index])
			}
			return strings.TrimSpace(forwarded)
		}
	}
	return remoteHost(request)
}

func remoteHost(request *http.Request) string {
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		return request.RemoteAddr
	}
	return host
}

func forwardedScheme(cfg *config.Config, request *http.Request) string {
	if request.TLS != nil {
		return "https"
	}
	if trustedProxy(cfg, request) && request.Header.Get("X-Forwarded-Proto") == "https" {
		return "https"
	}
	return "http"
}

func forwardedHost(cfg *config.Config, request *http.Request) string {
	if trustedProxy(cfg, request) {
		if host := request.Header.Get("X-Forwarded-Host"); host != "" {
			return host
		}
	}
	return request.Host
}

func withSameOrigin(cfg *config.Config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		site := request.Header.Get("Sec-Fetch-Site")
		if site == "cross-site" {
			writeError(writer, http.StatusForbidden, "Cross-site request blocked")
			return
		}
		if site == "" {
			origin := request.Header.Get("Origin")
			if origin != "" {
				expected := forwardedScheme(cfg, request) + "://" + forwardedHost(cfg, request)
				if origin != expected {
					writeError(writer, http.StatusForbidden, "Cross-origin request blocked")
					return
				}
			}
		}
		next.ServeHTTP(writer, request)
	})
}

func withAuth(cfg *config.Config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if cfg.AuthMode == "none" {
			next.ServeHTTP(writer, request)
			return
		}
		sess := sessionFrom(request)
		if sess != nil {
			if cfg.AuthMode == "password" && sess.Values["authenticated"] == true {
				next.ServeHTTP(writer, request)
				return
			}
			if cfg.AuthMode == "oidc" && sess.Values["user"] != nil {
				next.ServeHTTP(writer, request)
				return
			}
		}
		writeError(writer, http.StatusUnauthorized, "Unauthorized")
	})
}

func notFound(writer http.ResponseWriter, _ *http.Request) {
	writeError(writer, http.StatusNotFound, "Not found")
}
