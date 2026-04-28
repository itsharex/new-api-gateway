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
	h.mux.Handle("GET /admin/api/traces", h.auth.Middleware(h.auth.Require(PermissionViewNormalizedTraces, http.HandlerFunc(h.listTraces))))
	h.mux.Handle("GET /admin/api/anomalies", h.auth.Middleware(h.auth.Require(PermissionViewAggregates, http.HandlerFunc(h.listAnomalies))))
	h.mux.Handle("GET /admin/api/coverage-alerts", h.auth.Middleware(h.auth.Require(PermissionViewAggregates, http.HandlerFunc(h.listCoverageAlerts))))
	h.mux.Handle("POST /admin/api/reviews", h.auth.Middleware(h.auth.Require(PermissionReview, http.HandlerFunc(h.createReview))))
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

func (h Handler) listTraces(w http.ResponseWriter, r *http.Request) {
	filter := TraceFilter{
		TraceID:          r.URL.Query().Get("trace_id"),
		EmployeeNo:       r.URL.Query().Get("employee_no"),
		TokenFingerprint: r.URL.Query().Get("token_fingerprint"),
		RoutePattern:     r.URL.Query().Get("route_pattern"),
		Model:            r.URL.Query().Get("model"),
		Limit:            100,
	}
	items, err := h.repo.ListTraces(r.Context(), filter)
	if err != nil {
		http.Error(w, "failed to list traces", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"traces": items})
}

func (h Handler) listAnomalies(w http.ResponseWriter, r *http.Request) {
	items, err := h.repo.ListAnomalies(r.Context(), 100)
	if err != nil {
		http.Error(w, "failed to list anomalies", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"anomalies": items})
}

func (h Handler) listCoverageAlerts(w http.ResponseWriter, r *http.Request) {
	items, err := h.repo.ListCoverageAlerts(r.Context(), 100)
	if err != nil {
		http.Error(w, "failed to list coverage alerts", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"coverage_alerts": items})
}

func (h Handler) createReview(w http.ResponseWriter, r *http.Request) {
	principal, _ := PrincipalFromContext(r.Context())
	var input struct {
		TargetType string `json:"target_type"`
		TargetID   string `json:"target_id"`
		Decision   string `json:"decision"`
		Note       string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	now := h.auth.now()
	decision := ReviewDecision{
		TargetType:       input.TargetType,
		TargetID:         input.TargetID,
		Decision:         input.Decision,
		ReviewerID:       principal.UserID,
		ReviewerUsername: principal.Username,
		Note:             input.Note,
		CreatedAt:        now,
	}
	if err := h.repo.InsertReviewDecision(r.Context(), decision); err != nil {
		http.Error(w, "failed to create review", http.StatusInternalServerError)
		return
	}
	metadata, err := json.Marshal(map[string]string{"decision": input.Decision})
	if err != nil {
		metadata = []byte(`{}`)
	}
	_ = h.repo.InsertAuditActionLog(r.Context(), AuditActionLog{
		ActorUserID:   principal.UserID,
		ActorUsername: principal.Username,
		Action:        "review_decision_create",
		TargetType:    input.TargetType,
		TargetID:      input.TargetID,
		MetadataJSON:  string(metadata),
		CreatedAt:     now,
	})
	writeJSON(w, http.StatusCreated, map[string]any{"review": decision})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
