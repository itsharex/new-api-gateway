package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
)

type HandlerConfig struct {
	Repo Repository
	Auth Auth
}

type Handler struct {
	repo Repository
	auth Auth
	mux  *http.ServeMux
}

func NewHandler(cfg HandlerConfig) Handler {
	auth := cfg.Auth
	if auth.Repo.db == nil {
		auth.Repo = cfg.Repo
	}
	h := Handler{repo: cfg.Repo, auth: auth, mux: http.NewServeMux()}
	h.mux.HandleFunc("POST /admin/api/login", h.login)
	h.mux.Handle("GET /admin/api/me", h.auth.Middleware(http.HandlerFunc(h.me)))
	h.mux.HandleFunc("POST /admin/api/logout", h.logout)
	return h
}

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

func (h Handler) login(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	user, err := h.repo.FindActiveUserByUsername(r.Context(), input.Username)
	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}
	if err != nil {
		http.Error(w, "failed to load user", http.StatusInternalServerError)
		return
	}
	if CheckPassword(user.PasswordHash, input.Password) != nil {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}
	sessionID, err := NewSessionID()
	if err != nil {
		http.Error(w, "failed to create session", http.StatusInternalServerError)
		return
	}
	expiresAt := h.auth.now().Add(12 * time.Hour)
	if err := h.repo.CreateSession(r.Context(), Session{SessionID: sessionID, UserID: user.ID, ExpiresAt: expiresAt}); err != nil {
		http.Error(w, "failed to create session", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, h.auth.sessionCookie(sessionID, expiresAt))
	_ = h.repo.InsertAuditActionLog(r.Context(), AuditActionLog{
		ActorUserID:   user.ID,
		ActorUsername: user.Username,
		Action:        "login",
		TargetType:    "audit_user",
		TargetID:      user.Username,
		MetadataJSON:  `{"auth_provider":"local"}`,
		CreatedAt:     h.auth.now(),
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"user": Principal{UserID: user.ID, Username: user.Username, DisplayName: user.DisplayName, Role: user.Role},
	})
}

func (h Handler) me(w http.ResponseWriter, r *http.Request) {
	principal, _ := PrincipalFromContext(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"user": principal})
}

func (h Handler) logout(w http.ResponseWriter, r *http.Request) {
	now := h.auth.now()
	http.SetCookie(w, h.auth.clearCookie())

	if cookie, err := r.Cookie(h.auth.CookieName); err == nil {
		if sessionID, ok := h.auth.verifyCookie(cookie.Value); ok {
			principal, principalErr := h.repo.PrincipalBySession(r.Context(), sessionID, now)
			if err := h.repo.RevokeSession(r.Context(), sessionID, now); err != nil {
				http.Error(w, "failed to revoke session", http.StatusInternalServerError)
				return
			}
			if principalErr == nil {
				_ = h.repo.InsertAuditActionLog(r.Context(), AuditActionLog{
					ActorUserID:   principal.UserID,
					ActorUsername: principal.Username,
					Action:        "logout",
					TargetType:    "audit_user",
					TargetID:      principal.Username,
					CreatedAt:     now,
				})
			}
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
