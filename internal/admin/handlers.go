package admin

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/your-company/new-api-gateway/internal/authkeys"
	"github.com/your-company/new-api-gateway/internal/evidence"
	"github.com/your-company/new-api-gateway/internal/fingerprint"
)

type HandlerConfig struct {
	Repo          Repository
	Auth          Auth
	AuditSecret   string
	EvidenceStore evidence.Store
}

type Handler struct {
	repo          Repository
	auth          Auth
	auditSecret   string
	evidenceStore evidence.Store
	lookupLimiter RateLimiter
	rawLimiter    RateLimiter
}

func NewHandler(cfg HandlerConfig) Handler {
	auth := cfg.Auth
	if auth.Repo.db == nil {
		auth.Repo = cfg.Repo
	}
	h := Handler{
		repo:          cfg.Repo,
		auth:          auth,
		auditSecret:   cfg.AuditSecret,
		evidenceStore: cfg.EvidenceStore,
		lookupLimiter: NewMemoryRateLimiter(20, time.Hour),
		rawLimiter:    NewMemoryRateLimiter(120, time.Hour),
	}
	return h
}

func (h Handler) routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /admin/api/login", h.login)
	mux.Handle("GET /admin/api/me", h.auth.Middleware(http.HandlerFunc(h.me)))
	mux.Handle("POST /admin/api/me/password", h.auth.Middleware(h.requireCSRF(http.HandlerFunc(h.changePassword))))
	mux.Handle("GET /admin/api/overview", h.auth.Middleware(h.auth.Require(PermissionViewAggregates, http.HandlerFunc(h.overview))))
	mux.Handle("GET /admin/api/usage", h.auth.Middleware(h.auth.Require(PermissionViewAggregates, http.HandlerFunc(h.listUsage))))
	mux.Handle("GET /admin/api/traces/{trace_id}", h.auth.Middleware(h.auth.Require(PermissionViewNormalizedTraces, http.HandlerFunc(h.getTraceDetail))))
	mux.Handle("GET /admin/api/context-catalog", h.auth.Middleware(h.auth.Require(PermissionViewAggregates, http.HandlerFunc(h.listContextCatalog))))
	mux.Handle("POST /admin/api/context-catalog", h.auth.Middleware(h.requireCSRF(h.auth.Require(PermissionReview, http.HandlerFunc(h.createContextCatalogEntry)))))
	mux.Handle("GET /admin/api/audit-logs", h.auth.Middleware(h.auth.Require(PermissionManageUsers, http.HandlerFunc(h.listAuditLogs))))
	mux.HandleFunc("POST /admin/api/logout", h.logout)
	mux.Handle("GET /admin/api/traces", h.auth.Middleware(h.auth.Require(PermissionViewNormalizedTraces, http.HandlerFunc(h.listTraces))))
	mux.Handle("GET /admin/api/anomalies", h.auth.Middleware(h.auth.Require(PermissionViewAggregates, http.HandlerFunc(h.listAnomalies))))
	mux.Handle("GET /admin/api/coverage-alerts", h.auth.Middleware(h.auth.Require(PermissionViewAggregates, http.HandlerFunc(h.listCoverageAlerts))))
	mux.Handle("POST /admin/api/reviews", h.auth.Middleware(h.requireCSRF(h.auth.Require(PermissionReview, http.HandlerFunc(h.createReview)))))
	mux.Handle("POST /admin/api/api-key-lookup", h.auth.Middleware(h.requireCSRF(h.auth.Require(PermissionAPIKeyLookup, http.HandlerFunc(h.createAPIKeyLookup)))))
	mux.Handle("GET /admin/api/raw-evidence/{trace_id}/{object_type}", h.auth.Middleware(h.auth.Require(PermissionRawEvidence, http.HandlerFunc(h.getRawEvidence))))
	mux.Handle("GET /admin/api/token-identities", h.auth.Middleware(h.auth.Require(PermissionViewAggregates, http.HandlerFunc(h.listTokenIdentities))))
	mux.Handle("GET /admin/api/review-decisions", h.auth.Middleware(h.auth.Require(PermissionReview, http.HandlerFunc(h.listReviewDecisions))))
	mux.Handle("GET /admin/api/settings", h.auth.Middleware(h.auth.Require(PermissionManageUsers, http.HandlerFunc(h.systemSettings))))
	return mux
}

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.routes().ServeHTTP(w, r)
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
	csrfToken, err := NewCSRFToken()
	if err != nil {
		http.Error(w, "failed to create csrf token", http.StatusInternalServerError)
		return
	}
	expiresAt := h.auth.now().Add(12 * time.Hour)
	if err := h.repo.CreateSession(r.Context(), Session{SessionID: sessionID, UserID: user.ID, ExpiresAt: expiresAt, CSRFToken: csrfToken}); err != nil {
		http.Error(w, "failed to create session", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, h.auth.sessionCookie(sessionID, expiresAt))
	http.SetCookie(w, h.auth.csrfCookie(csrfToken, expiresAt))
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

func (h Handler) changePassword(w http.ResponseWriter, r *http.Request) {
	var input struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
		ConfirmPassword string `json:"confirm_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if input.CurrentPassword == "" || input.NewPassword == "" || input.ConfirmPassword == "" {
		http.Error(w, "password fields are required", http.StatusBadRequest)
		return
	}
	if utf8.RuneCountInString(input.NewPassword) < 12 {
		http.Error(w, "new password must be at least 12 characters", http.StatusBadRequest)
		return
	}
	if input.NewPassword != input.ConfirmPassword {
		http.Error(w, "new password confirmation does not match", http.StatusBadRequest)
		return
	}
	principal, ok := PrincipalFromContext(r.Context())
	if !ok {
		http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
		return
	}
	user, err := h.repo.FindActiveUserByID(r.Context(), principal.UserID)
	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
		return
	}
	if err != nil {
		http.Error(w, "failed to change password", http.StatusInternalServerError)
		return
	}
	if CheckPassword(user.PasswordHash, input.CurrentPassword) != nil {
		http.Error(w, "current password is incorrect", http.StatusUnauthorized)
		return
	}
	if CheckPassword(user.PasswordHash, input.NewPassword) == nil {
		http.Error(w, "new password must be different from current password", http.StatusBadRequest)
		return
	}
	newHash, err := HashPassword(input.NewPassword)
	if err != nil {
		http.Error(w, "failed to change password", http.StatusInternalServerError)
		return
	}
	now := h.auth.now()
	if err := h.repo.ChangeUserPassword(r.Context(), user.ID, newHash, principal.SessionID, AuditActionLog{
		ActorUserID:   user.ID,
		ActorUsername: user.Username,
		Action:        "password_changed",
		TargetType:    "audit_user",
		TargetID:      user.Username,
		MetadataJSON:  `{"revoked_other_sessions":true}`,
		CreatedAt:     now,
	}, now); err != nil {
		http.Error(w, "failed to change password", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h Handler) logout(w http.ResponseWriter, r *http.Request) {
	now := h.auth.now()
	http.SetCookie(w, h.auth.clearCookie())
	http.SetCookie(w, h.auth.clearCSRFCookie())

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

func (h Handler) requireCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		cookieName := h.auth.CSRFCookieName
		if cookieName == "" {
			cookieName = "audit_admin_csrf"
		}
		principal, ok := PrincipalFromContext(r.Context())
		if !ok || principal.CSRFToken == "" {
			http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
			return
		}
		cookie, err := r.Cookie(cookieName)
		headerToken := r.Header.Get("X-CSRF-Token")
		if err != nil ||
			cookie.Value == "" ||
			headerToken == "" ||
			subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(headerToken)) != 1 ||
			subtle.ConstantTimeCompare([]byte(headerToken), []byte(principal.CSRFToken)) != 1 {
			http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h Handler) listTraces(w http.ResponseWriter, r *http.Request) {
	filter := TraceFilter{
		TraceID:          r.URL.Query().Get("trace_id"),
		Username:         r.URL.Query().Get("username"),
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

func (h Handler) overview(w http.ResponseWriter, r *http.Request) {
	summary, err := h.repo.OverviewSummary(r.Context(), h.auth.now())
	if err != nil {
		http.Error(w, "failed to load overview", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"overview": summary})
}

func usageRangeWindow(value string, now time.Time) (string, time.Time, time.Time) {
	switch strings.TrimSpace(value) {
	case "1d":
		return "1d", now.AddDate(0, 0, -1), now
	case "7d":
		return "7d", now.AddDate(0, 0, -7), now
	default:
		return "30d", now.AddDate(0, 0, -30), now
	}
}

func (h Handler) listUsage(w http.ResponseWriter, r *http.Request) {
	username := strings.TrimSpace(r.URL.Query().Get("username"))
	model := strings.TrimSpace(r.URL.Query().Get("model"))
	filter := UsageFilter{
		Username:         username,
		TokenFingerprint: r.URL.Query().Get("token_fingerprint"),
		Model:            model,
		RoutePattern:     r.URL.Query().Get("route_pattern"),
		BucketSize:       r.URL.Query().Get("bucket_size"),
		Limit:            100,
	}
	items, err := h.repo.ListUsageAggregates(r.Context(), filter)
	if err != nil {
		http.Error(w, "failed to list usage", http.StatusInternalServerError)
		return
	}
	response := map[string]any{"usage": items}
	if username != "" {
		rangeValue, start, end := usageRangeWindow(r.URL.Query().Get("range"), h.auth.now())
		trend, err := h.repo.EmployeeUsageTrend(r.Context(), EmployeeUsageFilter{
			Username: username,
			Range:    rangeValue,
			Model:    model,
			Start:    start,
			End:      end,
		})
		if err != nil {
			http.Error(w, "failed to load employee usage", http.StatusInternalServerError)
			return
		}
		response["employee_usage"] = trend
	}
	writeJSON(w, http.StatusOK, response)
}

func (h Handler) listTokenIdentities(w http.ResponseWriter, r *http.Request) {
	items, err := h.repo.ListTokenIdentities(r.Context(), TokenIdentityFilter{
		Username:         r.URL.Query().Get("username"),
		TokenFingerprint: r.URL.Query().Get("token_fingerprint"),
		Limit:            100,
	})
	if err != nil {
		http.Error(w, "failed to list token identities", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token_identities": items})
}

func (h Handler) listReviewDecisions(w http.ResponseWriter, r *http.Request) {
	items, err := h.repo.ListReviewDecisions(r.Context(), ReviewDecisionFilter{
		TargetType: r.URL.Query().Get("target_type"),
		TargetID:   r.URL.Query().Get("target_id"),
		Limit:      100,
	})
	if err != nil {
		http.Error(w, "failed to list review decisions", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"review_decisions": items})
}

func (h Handler) systemSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"settings": SystemSettingsSummary{
		UsernamePattern: "configured by USERNAME_PATTERN",
		MetricsEnabled:  true,
		LookupLimit:     20,
		RawAccessLimit:  120,
	}})
}

func (h Handler) getTraceDetail(w http.ResponseWriter, r *http.Request) {
	traceID := strings.TrimSpace(r.PathValue("trace_id"))
	if traceID == "" {
		http.Error(w, "trace_id is required", http.StatusBadRequest)
		return
	}
	detail, err := h.repo.GetTraceDetail(r.Context(), traceID)
	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "trace not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "failed to load trace", http.StatusInternalServerError)
		return
	}
	detail.Anomalies = withAnomalyDisplayReasons(detail.Anomalies)
	if principal, ok := PrincipalFromContext(r.Context()); !ok || !principal.Role.Allows(PermissionRawEvidence) {
		detail.RequestRawRef = ""
		detail.ResponseRawRef = ""
		detail.RequestHeadersRef = ""
		detail.ResponseHeadersRef = ""
	}
	writeJSON(w, http.StatusOK, map[string]any{"trace": detail})
}

func (h Handler) listContextCatalog(w http.ResponseWriter, r *http.Request) {
	activeOnly := r.URL.Query().Get("active") != "false"
	items, err := h.repo.ListContextCatalog(r.Context(), activeOnly, 100)
	if err != nil {
		http.Error(w, "failed to list context catalog", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"context_catalog": items})
}

func (h Handler) createContextCatalogEntry(w http.ResponseWriter, r *http.Request) {
	principal, _ := PrincipalFromContext(r.Context())
	var input struct {
		ContextType            string   `json:"context_type"`
		Name                   string   `json:"name"`
		Description            string   `json:"description"`
		Keywords               []string `json:"keywords"`
		Aliases                []string `json:"aliases"`
		Owner                  string   `json:"owner"`
		ExpectedTaskCategories []string `json:"expected_task_categories"`
		ExpectedModels         []string `json:"expected_models"`
		ExpectedUsageLevel     string   `json:"expected_usage_level"`
		Active                 *bool    `json:"active"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	active := true
	if input.Active != nil {
		active = *input.Active
	}
	entry := ContextCatalogEntry{
		ContextType:            strings.TrimSpace(input.ContextType),
		Name:                   strings.TrimSpace(input.Name),
		Description:            input.Description,
		Keywords:               input.Keywords,
		Aliases:                input.Aliases,
		Owner:                  input.Owner,
		ExpectedTaskCategories: input.ExpectedTaskCategories,
		ExpectedModels:         input.ExpectedModels,
		ExpectedUsageLevel:     strings.TrimSpace(input.ExpectedUsageLevel),
		Active:                 active,
		CreatedBy:              principal.Username,
		UpdatedBy:              principal.Username,
	}
	if !validContextCatalogEntry(entry) {
		http.Error(w, "invalid context catalog entry", http.StatusBadRequest)
		return
	}
	if err := h.repo.InsertContextCatalogEntry(r.Context(), entry); err != nil {
		http.Error(w, "failed to save context catalog entry", http.StatusInternalServerError)
		return
	}
	if err := h.repo.InsertAuditActionLog(r.Context(), AuditActionLog{
		ActorUserID:   principal.UserID,
		ActorUsername: principal.Username,
		Action:        "context_catalog_upsert",
		TargetType:    "context_catalog",
		TargetID:      entry.ContextType + ":" + entry.Name,
		MetadataJSON:  `{"source":"admin_api"}`,
		CreatedAt:     h.auth.now(),
	}); err != nil {
		http.Error(w, "failed to audit context catalog entry", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"context": entry})
}

func validContextCatalogEntry(input ContextCatalogEntry) bool {
	switch input.ContextType {
	case "repo", "project", "product", "service", "team", "keyword_set":
	default:
		return false
	}
	if input.Name == "" {
		return false
	}
	switch input.ExpectedUsageLevel {
	case "", "low", "medium", "high":
		return true
	default:
		return false
	}
}

func (h Handler) listAuditLogs(w http.ResponseWriter, r *http.Request) {
	items, err := h.repo.ListAuditActionLogs(r.Context(), 100)
	if err != nil {
		http.Error(w, "failed to list audit logs", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"audit_logs": items})
}

func (h Handler) listAnomalies(w http.ResponseWriter, r *http.Request) {
	items, err := h.repo.ListAnomalies(r.Context(), 100)
	if err != nil {
		http.Error(w, "failed to list anomalies", http.StatusInternalServerError)
		return
	}
	items = withAnomalyDisplayReasons(items)
	writeJSON(w, http.StatusOK, map[string]any{"anomalies": items})
}

func withAnomalyDisplayReasons(items []AnomalySummary) []AnomalySummary {
	if len(items) == 0 {
		return items
	}
	result := make([]AnomalySummary, 0, len(items))
	for _, item := range items {
		item.DisplayReason = formatAnomalyDisplayReasonZH(item)
		result = append(result, item)
	}
	return result
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
	input.TargetType = strings.TrimSpace(input.TargetType)
	input.TargetID = strings.TrimSpace(input.TargetID)
	input.Decision = strings.TrimSpace(input.Decision)
	if !validReviewTargetType(input.TargetType) || input.TargetID == "" || !validReviewDecision(input.Decision) {
		http.Error(w, "invalid review input", http.StatusBadRequest)
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

func (h Handler) createAPIKeyLookup(w http.ResponseWriter, r *http.Request) {
	principal, _ := PrincipalFromContext(r.Context())
	if !h.lookupLimiter.Allow(principal.Username+":api_key_lookup", h.auth.now()) {
		http.Error(w, http.StatusText(http.StatusTooManyRequests), http.StatusTooManyRequests)
		return
	}
	var input struct {
		APIKey string `json:"api_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	canonical := canonicalizeLookupKey(input.APIKey)
	input.APIKey = ""
	if canonical == "" {
		http.Error(w, "api_key is required", http.StatusBadRequest)
		return
	}
	fp := fingerprint.Compute(canonical, h.auditSecret)
	canonical = ""
	summary, err := h.repo.LookupTokenSummary(r.Context(), fp.Value, fp.Display)
	if err != nil {
		http.Error(w, "failed to lookup token", http.StatusInternalServerError)
		return
	}
	_ = h.repo.InsertAuditActionLog(r.Context(), AuditActionLog{
		ActorUserID:        principal.UserID,
		ActorUsername:      principal.Username,
		Action:             "api_key_lookup",
		TargetType:         "token",
		TargetID:           fp.Display,
		TokenFingerprint:   fp.Value,
		FingerprintDisplay: fp.Display,
		MetadataJSON:       `{"plaintext_discarded":true}`,
		CreatedAt:          h.auth.now(),
	})
	writeJSON(w, http.StatusOK, map[string]any{"lookup": summary})
}

func canonicalizeLookupKey(value string) string {
	canonical, ok := authkeys.Canonicalize(value)
	if !ok {
		return ""
	}
	return canonical
}

func (h Handler) getRawEvidence(w http.ResponseWriter, r *http.Request) {
	principal, _ := PrincipalFromContext(r.Context())
	if !h.rawLimiter.Allow(principal.Username+":raw_evidence", h.auth.now()) {
		http.Error(w, http.StatusText(http.StatusTooManyRequests), http.StatusTooManyRequests)
		return
	}
	traceID := strings.TrimSpace(r.PathValue("trace_id"))
	objectType := strings.TrimSpace(r.PathValue("object_type"))
	if traceID == "" || objectType == "" {
		http.Error(w, "raw evidence path is required", http.StatusBadRequest)
		return
	}
	objectRef := strings.TrimSpace(r.URL.Query().Get("object_ref"))
	if objectRef == "" {
		objectRef = strings.TrimSpace(r.URL.Query().Get("ref"))
	}
	object, err := h.repo.FindRawEvidenceObject(r.Context(), traceID, objectType, objectRef)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
		return
	}
	if h.evidenceStore == nil {
		http.Error(w, "raw evidence store unavailable", http.StatusServiceUnavailable)
		return
	}
	reader, err := h.evidenceStore.Get(r.Context(), object.ObjectRef)
	if err != nil {
		http.Error(w, "failed to load raw evidence", http.StatusInternalServerError)
		return
	}
	defer reader.Close()

	metadata, err := json.Marshal(map[string]string{
		"object_type": object.ObjectType,
		"object_ref":  object.ObjectRef,
	})
	if err != nil {
		metadata = []byte(`{}`)
	}
	if err := h.repo.InsertAuditActionLog(r.Context(), AuditActionLog{
		ActorUserID:   principal.UserID,
		ActorUsername: principal.Username,
		Action:        "raw_evidence_access",
		TargetType:    "raw_evidence",
		TargetID:      object.ObjectType,
		TraceID:       object.TraceID,
		MetadataJSON:  string(metadata),
		CreatedAt:     h.auth.now(),
	}); err != nil {
		http.Error(w, "failed to audit raw evidence access", http.StatusInternalServerError)
		return
	}
	if object.ContentType != "" {
		w.Header().Set("Content-Type", object.ContentType)
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if object.SHA256 != "" {
		w.Header().Set("X-Audit-Evidence-SHA256", object.SHA256)
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, reader)
}

func validReviewTargetType(targetType string) bool {
	switch targetType {
	case "trace", "anomaly", "coverage_alert":
		return true
	default:
		return false
	}
}

func validReviewDecision(decision string) bool {
	switch decision {
	case "acknowledge", "dismiss", "confirm", "mark_personal_use", "mark_abuse", "needs_normalizer", "mark_fixed", "ignore_for_now":
		return true
	default:
		return false
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
