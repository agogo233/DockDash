package server

import (
	"database/sql"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"dockdash/internal/assets"
	"dockdash/internal/config"
	"dockdash/internal/db"
	"dockdash/internal/session"
)

type API struct {
	cfg   *config.Config
	db    *sql.DB
	store *session.Store
	ready atomic.Bool
}

func New(cfg *config.Config, database *sql.DB, store *session.Store) *API {
	return &API{cfg: cfg, db: database, store: store}
}

func (a *API) MarkReady()    { a.ready.Store(true) }
func (a *API) MarkNotReady() { a.ready.Store(false) }

func (a *API) Handler() http.Handler {
	apiLimiter := newLimiter(300, time.Minute)
	apiMux := http.NewServeMux()
	apiMux.HandleFunc("GET /api/health/live", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, http.StatusOK, map[string]string{
			"status":    "ok",
			"timestamp": time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		})
	})
	apiMux.HandleFunc("GET /api/health", a.readyHandler)
	apiMux.HandleFunc("GET /api/health/ready", a.readyHandler)
	protected := http.NewServeMux()
	protected.HandleFunc("GET /api/config", a.configHandler)
	apiMux.Handle("/api/", withSameOrigin(a.cfg, withAuth(a.cfg, protected)))

	apiHandler := withSession(a.store, withBodyLimit(100*1024, withRateLimit(apiLimiter, a.cfg, apiMux)))

	authHandlers := NewAuth(a.cfg, a.store)
	authMux := http.NewServeMux()
	authMux.HandleFunc("GET /auth/me", authHandlers.Me)
	authMux.HandleFunc("GET /auth/login", authHandlers.Login)
	authMux.HandleFunc("GET /auth/callback", authHandlers.Callback)
	authMux.HandleFunc("POST /auth/logout", authHandlers.Logout)
	authMux.HandleFunc("POST /auth/password/login", withSameOrigin(a.cfg,
		withRateLimitMessage(newLimiter(5, 15*time.Minute), a.cfg,
			http.HandlerFunc(authHandlers.PasswordLogin), authRateLimitMessage)).ServeHTTP)
	authHandler := withSession(a.store, withBodyLimit(100*1024,
		withRateLimitMessage(newLimiter(20, 15*time.Minute), a.cfg, authMux, authRateLimitMessage)))

	web, err := fs.Sub(assets.Web, "web")
	if err != nil {
		panic(err)
	}
	root := http.NewServeMux()
	root.Handle("/auth/", authHandler)
	root.Handle("/api/", apiHandler)
	root.Handle("/", spaHandler(a.cfg, web, http.FileServer(http.FS(web)), newLimiter(300, time.Minute)))
	return root
}

func (a *API) configHandler(writer http.ResponseWriter, _ *http.Request) {
	cfg := a.cfg
	writeJSON(writer, http.StatusOK, map[string]any{
		"version":                     cfg.AppVersion,
		"appriseConfigured":           cfg.AppriseURL != "",
		"certVaultConfigured":         cfg.CertVaultURL != "" && cfg.CertVaultAPIKeyValue() != "",
		"certVaultUrl":                nullableString(cfg.CertVaultURL),
		"dockerHosts":                 cfg.DockerHostEntries(),
		"kubernetesEnabled":           cfg.KubernetesEnabled,
		"kubernetesContexts":          cfg.KubernetesContexts,
		"kubernetesNamespaces":        cfg.KubernetesNamespaces,
		"networkCidrs":                cfg.NetworkCIDRs,
		"healthCheckInterval":         cfg.HealthCheckInterval,
		"resourceMonitorInterval":     cfg.ResourceMonitorInterval,
		"updateCheckInterval":         cfg.UpdateCheckInterval,
		"certificateCheckInterval":    cfg.CertificateCheckInterval,
		"certificateExpiryThresholds": cfg.CertificateExpiryThresholds,
		"containerControlsEnabled":    cfg.ContainerControlsEnabled,
		"healthHistoryEnabled":        cfg.HealthHistoryEnabled,
		"healthHistoryTtlDays":        cfg.HealthHistoryTTLDays,
		"resourceMonitorEnabled":      cfg.ResourceMonitorEnabled,
		"cpuSpikeThreshold":           cfg.CPUSpikeThreshold,
		"memorySpikeThreshold":        cfg.MemorySpikeThreshold,
		"spikeDurationThreshold":      cfg.SpikeDurationThreshold,
		"fileExplorerEnabled":         cfg.FileExplorerEnabled,
		"terminalEnabled":             cfg.TerminalEnabled,
	})
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func (a *API) readyHandler(writer http.ResponseWriter, _ *http.Request) {
	databaseHealthy := db.Healthy(a.db)
	ready := a.ready.Load() && databaseHealthy
	status := "not_ready"
	databaseStatus := "down"
	if ready {
		status = "ready"
	}
	if databaseHealthy {
		databaseStatus = "up"
	}
	writeJSON(writer, map[bool]int{true: http.StatusOK, false: http.StatusServiceUnavailable}[ready], map[string]any{
		"status":    status,
		"checks":    map[string]string{"database": databaseStatus},
		"timestamp": time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
	})
}

func spaHandler(cfg *config.Config, web fs.FS, files http.Handler, l *limiter) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			notFound(writer, request)
			return
		}
		name := strings.TrimPrefix(request.URL.Path, "/")
		if name != "" {
			if info, err := fs.Stat(web, name); err == nil && !info.IsDir() {
				files.ServeHTTP(writer, request)
				return
			}
		}
		allowed, remaining, reset := l.allow(clientIP(cfg, request))
		writer.Header().Set("RateLimit", "limit="+strconv.Itoa(l.limit)+", remaining="+strconv.Itoa(remaining)+", reset="+strconv.Itoa(reset))
		writer.Header().Set("RateLimit-Policy", strconv.Itoa(l.limit)+";w="+strconv.Itoa(reset))
		if !allowed {
			http.Error(writer, "Too many requests, please try again later", http.StatusTooManyRequests)
			return
		}
		index, err := fs.ReadFile(web, "index.html")
		if err != nil {
			http.Error(writer, "Not found", http.StatusNotFound)
			return
		}
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		writer.WriteHeader(http.StatusOK)
		if request.Method == http.MethodHead {
			return
		}
		_, _ = writer.Write(index)
	})
}
