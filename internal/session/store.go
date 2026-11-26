package session

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const cookieName = "connect.sid"

type Session struct {
	ID      string
	Values  map[string]any
	Expires time.Time
	MaxAge  int
}

type Store struct {
	db     *sql.DB
	secret string
	maxAge int
	secure bool
}

func New(database *sql.DB, secret string, maxAge int, secure bool) (*Store, error) {
	const schema = `CREATE TABLE IF NOT EXISTS sessions
	(
		sid TEXT NOT NULL PRIMARY KEY,
		sess JSON NOT NULL,
		expire TEXT NOT NULL
	)`
	if _, err := database.Exec(schema); err != nil {
		return nil, err
	}
	return &Store{db: database, secret: secret, maxAge: maxAge, secure: secure}, nil
}

func generateID() string {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}

func signature(id, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(id))
	encoded := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	return strings.TrimRight(encoded, "=")
}

func (s *Store) newSession() *Session {
	return &Session{
		ID:      generateID(),
		Values:  map[string]any{},
		Expires: time.Now().Add(time.Duration(s.maxAge) * time.Millisecond),
		MaxAge:  s.maxAge,
	}
}

func (s *Store) Get(request *http.Request) *Session {
	cookie, err := request.Cookie(cookieName)
	if err != nil {
		return nil
	}
	decoded, err := url.QueryUnescape(cookie.Value)
	if err != nil || !strings.HasPrefix(decoded, "s:") {
		return nil
	}
	value := strings.TrimPrefix(decoded, "s:")
	dot := strings.LastIndex(value, ".")
	if dot <= 0 {
		return nil
	}
	id, sig := value[:dot], value[dot+1:]
	if !hmac.Equal([]byte(signature(id, s.secret)), []byte(sig)) {
		return nil
	}
	var raw string
	var expire string
	err = s.db.QueryRow(`SELECT sess, expire FROM sessions WHERE sid = ?`, id).Scan(&raw, &expire)
	if err != nil {
		return nil
	}
	parsedExpire, err := time.Parse(time.RFC3339Nano, expire)
	if err != nil {
		return nil
	}
	if time.Now().After(parsedExpire) {
		return nil
	}
	values := map[string]any{}
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil
	}
	return &Session{ID: id, Values: values, Expires: parsedExpire, MaxAge: s.maxAge}
}

func (s *Store) Save(writer http.ResponseWriter, sess *Session) error {
	if sess.ID == "" {
		sess.ID = generateID()
	}
	if sess.Expires.IsZero() {
		sess.Expires = time.Now().Add(time.Duration(s.maxAge) * time.Millisecond)
	}
	if sess.Values == nil {
		sess.Values = map[string]any{}
	}
	payload, err := json.Marshal(sess.Values)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(
		`INSERT INTO sessions (sid, sess, expire) VALUES (?, ?, ?)
		 ON CONFLICT(sid) DO UPDATE SET sess = excluded.sess, expire = excluded.expire`,
		sess.ID, string(payload), sess.Expires.Format(time.RFC3339Nano),
	)
	if err != nil {
		return err
	}
	s.writeCookie(writer, sess.ID)
	return nil
}

func (s *Store) writeCookie(writer http.ResponseWriter, id string) {
	value := url.QueryEscape("s:" + id + "." + signature(id, s.secret))
	cookie := &http.Cookie{
		Name:     cookieName,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.secure,
	}
	if s.maxAge > 0 {
		cookie.MaxAge = s.maxAge / 1000
	}
	http.SetCookie(writer, cookie)
}

func (s *Store) Regenerate(writer http.ResponseWriter, request *http.Request, sess *Session) (*Session, error) {
	if current := s.Get(request); current != nil {
		if _, err := s.db.Exec(`DELETE FROM sessions WHERE sid = ?`, current.ID); err != nil {
			return nil, err
		}
	}
	next := s.newSession()
	for key, value := range sess.Values {
		next.Values[key] = value
	}
	if err := s.Save(writer, next); err != nil {
		return nil, err
	}
	return next, nil
}

func (s *Store) Destroy(writer http.ResponseWriter, request *http.Request) error {
	if current := s.Get(request); current != nil {
		if _, err := s.db.Exec(`DELETE FROM sessions WHERE sid = ?`, current.ID); err != nil {
			return err
		}
	}
	http.SetCookie(writer, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.secure,
		MaxAge:   -1,
	})
	return nil
}

func (s *Store) StartCleanup(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(15 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := s.db.Exec(`DELETE FROM sessions WHERE datetime('now') > datetime(expire)`); err != nil {
					log.Printf("session cleanup failed: %v", err)
				}
			}
		}
	}()
}

var ErrNoSession = errors.New("no session")
