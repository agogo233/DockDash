package config

import (
	"encoding/hex"
	"os"
	"strings"
	"testing"

	"golang.org/x/crypto/scrypt"
)

func resetAuthEnv(t *testing.T) {
	t.Helper()
	keys := []string{
		"AUTH_USERNAME", "AUTH_PASSWORD", "AUTH_PASSWORD_HASH",
		"OIDC_ISSUER", "OIDC_CLIENT_ID", "OIDC_CLIENT_SECRET", "OIDC_REDIRECT_URI", "OIDC_SCOPES",
		"SESSION_SECRET", "DB_PATH", "MIGRATIONS_DIR",
	}
	for _, key := range keys {
		name := key
		old, existed := os.LookupEnv(name)
		t.Cleanup(func() {
			if existed {
				_ = os.Setenv(name, old)
			} else {
				_ = os.Unsetenv(name)
			}
		})
		_ = os.Unsetenv(name)
	}
}

func makePasswordHash(t *testing.T, password string) string {
	t.Helper()
	salt := []byte("0123456789abcdef")
	derived, err := scrypt.Key([]byte(password), salt, 16384, 8, 1, 64)
	if err != nil {
		t.Fatal(err)
	}
	return "scrypt$16384$8$1$" + hex.EncodeToString(salt) + "$" + hex.EncodeToString(derived)
}

func TestPasswordAuthDisabledByDefault(t *testing.T) {
	resetAuthEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PasswordAuthEnabled || cfg.AuthMode != "none" {
		t.Fatalf("expected auth disabled, got %+v", cfg.AuthMode)
	}
}

func TestPlaintextPasswordAuth(t *testing.T) {
	resetAuthEnv(t)
	t.Setenv("AUTH_USERNAME", "admin")
	t.Setenv("AUTH_PASSWORD", "secret")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.PasswordAuthEnabled || cfg.AuthMode != "password" {
		t.Fatal("expected password mode")
	}
	if !cfg.VerifyPassword("admin", "secret") || cfg.VerifyPassword("admin", "wrong") {
		t.Fatal("password verification failed")
	}
	if cfg.VerifyPassword("other", "secret") {
		t.Fatal("wrong username accepted")
	}
}

func TestPasswordHashTakesPrecedence(t *testing.T) {
	resetAuthEnv(t)
	t.Setenv("AUTH_USERNAME", "admin")
	t.Setenv("AUTH_PASSWORD", "plaintext")
	t.Setenv("AUTH_PASSWORD_HASH", makePasswordHash(t, "hashed-pw"))
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.VerifyPassword("admin", "hashed-pw") || cfg.VerifyPassword("admin", "plaintext") {
		t.Fatal("hash precedence failed")
	}
}

func TestPasswordAuthTakesPrecedenceOverOIDC(t *testing.T) {
	resetAuthEnv(t)
	t.Setenv("AUTH_USERNAME", "admin")
	t.Setenv("AUTH_PASSWORD", "secret")
	t.Setenv("OIDC_ISSUER", "https://issuer.example")
	t.Setenv("OIDC_CLIENT_ID", "client")
	t.Setenv("OIDC_CLIENT_SECRET", "secret")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AuthMode != "password" {
		t.Fatalf("expected password mode, got %s", cfg.AuthMode)
	}
}

func TestPasswordAuthIgnoresPartialOIDCConfig(t *testing.T) {
	resetAuthEnv(t)
	t.Setenv("AUTH_USERNAME", "admin")
	t.Setenv("AUTH_PASSWORD", "secret")
	t.Setenv("OIDC_ISSUER", "https://issuer.example")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("partial OIDC config must be ignored: %v", err)
	}
	if cfg.AuthMode != "password" || cfg.OIDCEnabled {
		t.Fatalf("unexpected auth state: %s, oidc=%v", cfg.AuthMode, cfg.OIDCEnabled)
	}
}

func TestIncompletePasswordConfigIsRejected(t *testing.T) {
	resetAuthEnv(t)
	t.Setenv("AUTH_USERNAME", "admin")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "Incomplete password authentication") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInvalidPasswordHashIsRejected(t *testing.T) {
	resetAuthEnv(t)
	t.Setenv("AUTH_USERNAME", "admin")
	t.Setenv("AUTH_PASSWORD_HASH", "scrypt$not-valid")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "Invalid AUTH_PASSWORD_HASH") {
		t.Fatalf("unexpected error: %v", err)
	}
}
