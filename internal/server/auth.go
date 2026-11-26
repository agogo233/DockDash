package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"dockdash/internal/config"
	"dockdash/internal/session"
)

const authRateLimitMessage = "Too many authentication attempts, please try again later"

type AuthHandlers struct {
	cfg           *config.Config
	store         *session.Store
	oidcMu        sync.Mutex
	cachedProvider *oidc.Provider
}

func NewAuth(cfg *config.Config, store *session.Store) *AuthHandlers {
	return &AuthHandlers{cfg: cfg, store: store}
}

func randomToken(size int) string {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}

func (a *AuthHandlers) provider(ctx context.Context) (*oidc.Provider, error) {
	a.oidcMu.Lock()
	defer a.oidcMu.Unlock()
	if a.cachedProvider != nil {
		return a.cachedProvider, nil
	}
	timeoutCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	provider, err := oidc.NewProvider(timeoutCtx, a.cfg.OIDCIssuer)
	if err != nil {
		return nil, err
	}
	a.cachedProvider = provider
	return provider, nil
}

func (a *AuthHandlers) callbackURL(request *http.Request) string {
	if a.cfg.OIDCRedirectURI != "" {
		return a.cfg.OIDCRedirectURI
	}
	return forwardedScheme(a.cfg, request) + "://" + forwardedHost(a.cfg, request) + "/auth/callback"
}

func (a *AuthHandlers) oauthConfig(request *http.Request, provider *oidc.Provider) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     a.cfg.OIDCClientID,
		ClientSecret: a.cfg.OIDCClientSecret,
		Endpoint:     provider.Endpoint(),
		RedirectURL:  a.callbackURL(request),
		Scopes:       strings.Fields(a.cfg.OIDCScopes),
	}
}

func (a *AuthHandlers) Me(writer http.ResponseWriter, request *http.Request) {
	mode := a.cfg.AuthMode
	if mode == "none" {
		writeJSON(writer, http.StatusOK, map[string]any{"enabled": false, "mode": "none", "user": nil})
		return
	}
	var user any
	sess := sessionFrom(request)
	if mode == "password" {
		if sess != nil && sess.Values["authenticated"] == true {
			user = map[string]any{"sub": "local", "name": a.cfg.AuthUsername}
		}
	} else if sess != nil {
		user = sess.Values["user"]
	}
	writeJSON(writer, http.StatusOK, map[string]any{"enabled": true, "mode": mode, "user": user})
}

func (a *AuthHandlers) PasswordLogin(writer http.ResponseWriter, request *http.Request) {
	if !a.cfg.PasswordAuthEnabled {
		writeError(writer, http.StatusForbidden, "Password authentication is not enabled")
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeError(writer, http.StatusBadRequest, "Invalid request")
		return
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		writeError(writer, http.StatusBadRequest, "Invalid request")
		return
	}
	body.Username = strings.TrimSpace(body.Username)
	body.Password = strings.TrimSpace(body.Password)
	if body.Username == "" || body.Password == "" || len(body.Username) > 1024 || len(body.Password) > 1024 {
		writeError(writer, http.StatusBadRequest, "Invalid request")
		return
	}
	if !a.cfg.VerifyPassword(body.Username, body.Password) {
		safeUsername := strings.NewReplacer("\r", "", "\n", "", "\t", "").Replace(body.Username)
		log.Printf("[Auth] password login failed for %q from %s", safeUsername, clientIP(a.cfg, request))
		time.Sleep(500 * time.Millisecond)
		writeError(writer, http.StatusUnauthorized, "Invalid username or password")
		return
	}
	base := sessionFrom(request)
	if base == nil {
		base = a.store.Blank()
	}
	base.Values["authenticated"] = true
	base.Values["user"] = map[string]any{"sub": "local", "name": a.cfg.AuthUsername}
	if _, err := a.store.Regenerate(writer, request, base); err != nil {
		log.Printf("[Auth] session save failed: %v", err)
		writeError(writer, http.StatusInternalServerError, "Unable to save session")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]bool{"ok": true})
}

func (a *AuthHandlers) Logout(writer http.ResponseWriter, request *http.Request) {
	if err := a.store.Destroy(writer, request); err != nil {
		log.Printf("[Auth] session destroy failed: %v", err)
	}
	writeJSON(writer, http.StatusOK, map[string]bool{"ok": true})
}

func (a *AuthHandlers) Login(writer http.ResponseWriter, request *http.Request) {
	if !a.cfg.OIDCEnabled {
		http.Redirect(writer, request, "/", http.StatusFound)
		return
	}
	provider, err := a.provider(request.Context())
	if err != nil {
		log.Printf("OIDC login error: %v", err)
		writeError(writer, http.StatusInternalServerError, "OIDC configuration error")
		return
	}
	verifier := randomToken(32)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	state := randomToken(32)
	base := sessionFrom(request)
	if base == nil {
		base = a.store.Blank()
	}
	base.Values["oidcCodeVerifier"] = verifier
	base.Values["oidcState"] = state
	if err := a.store.Save(writer, base); err != nil {
		log.Printf("OIDC login error: %v", err)
		writeError(writer, http.StatusInternalServerError, "OIDC configuration error")
		return
	}
	url := a.oauthConfig(request, provider).AuthCodeURL(state,
		oauth2.SetAuthURLParam("code_challenge", challenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)
	http.Redirect(writer, request, url, http.StatusFound)
}

func (a *AuthHandlers) Callback(writer http.ResponseWriter, request *http.Request) {
	sess := sessionFrom(request)
	if sess == nil {
		http.Redirect(writer, request, "/login?error=invalid_state", http.StatusFound)
		return
	}
	state, stateOK := sess.Values["oidcState"].(string)
	verifier, verifierOK := sess.Values["oidcCodeVerifier"].(string)
	if !stateOK || !verifierOK || state == "" || verifier == "" || request.URL.Query().Get("state") != state {
		http.Redirect(writer, request, "/login?error=invalid_state", http.StatusFound)
		return
	}
	provider, err := a.provider(request.Context())
	if err != nil {
		log.Printf("OIDC callback error: %v", err)
		http.Redirect(writer, request, "/login?error=callback_failed", http.StatusFound)
		return
	}
	token, err := a.oauthConfig(request, provider).Exchange(
		request.Context(),
		request.URL.Query().Get("code"),
		oauth2.SetAuthURLParam("code_verifier", verifier),
	)
	if err != nil {
		log.Printf("OIDC callback error: %v", err)
		http.Redirect(writer, request, "/login?error=callback_failed", http.StatusFound)
		return
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		log.Print("OIDC callback error: no ID token claims in response")
		http.Redirect(writer, request, "/login?error=callback_failed", http.StatusFound)
		return
	}
	idToken, err := provider.Verifier(&oidc.Config{ClientID: a.cfg.OIDCClientID}).Verify(request.Context(), rawIDToken)
	if err != nil {
		log.Printf("OIDC callback error: %v", err)
		http.Redirect(writer, request, "/login?error=callback_failed", http.StatusFound)
		return
	}
	var claims struct {
		Sub     string `json:"sub"`
		Name    string `json:"name"`
		Email   string `json:"email"`
		Picture string `json:"picture"`
	}
	if err := idToken.Claims(&claims); err != nil {
		log.Printf("OIDC callback error: %v", err)
		http.Redirect(writer, request, "/login?error=callback_failed", http.StatusFound)
		return
	}
	user := map[string]any{"sub": claims.Sub}
	if claims.Name != "" {
		user["name"] = claims.Name
	}
	if claims.Email != "" {
		user["email"] = claims.Email
	}
	if claims.Picture != "" {
		user["picture"] = claims.Picture
	}
	delete(sess.Values, "oidcState")
	delete(sess.Values, "oidcCodeVerifier")
	sess.Values["user"] = user
	if _, err := a.store.Regenerate(writer, request, sess); err != nil {
		log.Printf("OIDC callback error: %v", err)
		http.Redirect(writer, request, "/login?error=callback_failed", http.StatusFound)
		return
	}
	http.Redirect(writer, request, "/", http.StatusFound)
}
