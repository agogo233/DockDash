package server

import (
	"context"
	"encoding/json"
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

func withRateLimit(l *limiter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		allowed, remaining, reset := l.allow(clientIP(request))
		writer.Header().Set("RateLimit", "limit="+strconv.Itoa(l.limit)+", remaining="+strconv.Itoa(remaining)+", reset="+strconv.Itoa(reset))
		writer.Header().Set("RateLimit-Policy", strconv.Itoa(l.limit)+";w="+strconv.Itoa(reset))
		if !allowed {
			writeError(writer, http.StatusTooManyRequests, "Too many API requests, please try again later")
			return
		}
		next.ServeHTTP(writer, request)
	})
}

func clientIP(request *http.Request) string {
	host := request.RemoteAddr
	if index := strings.LastIndex(host, ":"); index >= 0 {
		host = host[:index]
	}
	return host
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
				host := request.Header.Get("X-Forwarded-Host")
				if host == "" {
					host = request.Host
				}
				scheme := "http"
				if request.TLS != nil || request.Header.Get("X-Forwarded-Proto") == "https" {
					scheme = "https"
				}
				if origin != scheme+"://"+host {
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
