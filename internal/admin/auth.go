package admin

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strings"
	"time"
)

type principalContextKey struct{}

type Auth struct {
	Repo          Repository
	SessionSecret string
	CookieName    string
	CookieSecure  bool
	Now           func() time.Time
}

func (a Auth) now() time.Time {
	if a.Now != nil {
		return a.Now().UTC()
	}
	return time.Now().UTC()
}

func NewSessionID() (string, error) {
	var bytes [32]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return "sess_" + hex.EncodeToString(bytes[:]), nil
}

func (a Auth) sessionCookie(sessionID string, expiresAt time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     a.CookieName,
		Value:    a.signSessionID(sessionID),
		Path:     "/admin",
		Expires:  expiresAt.UTC(),
		HttpOnly: true,
		Secure:   a.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	}
}

func (a Auth) clearCookie() *http.Cookie {
	return &http.Cookie{
		Name:     a.CookieName,
		Value:    "",
		Path:     "/admin",
		Expires:  time.Unix(0, 0).UTC(),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   a.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	}
}

func (a Auth) signSessionID(sessionID string) string {
	mac := hmac.New(sha256.New, []byte(a.SessionSecret))
	_, _ = mac.Write([]byte(sessionID))
	signature := mac.Sum(nil)
	payload := sessionID + "." + base64.RawURLEncoding.EncodeToString(signature)
	return base64.RawURLEncoding.EncodeToString([]byte(payload))
}

func (a Auth) verifyCookie(value string) (string, bool) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return "", false
	}
	payload := string(decoded)
	sessionID, signatureValue, ok := strings.Cut(payload, ".")
	if !ok || sessionID == "" || signatureValue == "" {
		return "", false
	}
	signature, err := base64.RawURLEncoding.DecodeString(signatureValue)
	if err != nil {
		return "", false
	}
	mac := hmac.New(sha256.New, []byte(a.SessionSecret))
	_, _ = mac.Write([]byte(sessionID))
	expected := mac.Sum(nil)
	if subtle.ConstantTimeCompare(signature, expected) != 1 {
		return "", false
	}
	return sessionID, true
}

func (a Auth) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(a.CookieName)
		if err != nil {
			http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
			return
		}
		sessionID, ok := a.verifyCookie(cookie.Value)
		if !ok {
			http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
			return
		}
		principal, err := a.Repo.PrincipalBySession(r.Context(), sessionID, a.now())
		if err != nil {
			http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), principal)))
	})
}

func (a Auth) Require(permission Permission, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := PrincipalFromContext(r.Context())
		if !ok {
			http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
			return
		}
		if !principal.Role.Allows(permission) {
			http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func WithPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, principalContextKey{}, principal)
}

func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(principalContextKey{}).(Principal)
	return principal, ok
}
