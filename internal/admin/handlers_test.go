package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/your-company/new-api-gateway/internal/evidence"
	"github.com/your-company/new-api-gateway/internal/fingerprint"
)

func TestLoginMeLogoutFlow(t *testing.T) {
	passwordHash, err := HashPassword("secret-password")
	if err != nil {
		t.Fatalf("HashPassword error: %v", err)
	}
	db := &memoryAdminDB{
		user: User{ID: 1, Username: "alice", PasswordHash: passwordHash, DisplayName: "Alice", Role: RoleAuditor, Status: "active"},
	}
	repo := NewRepository(db)
	auth := Auth{
		Repo:          repo,
		SessionSecret: "session-secret-0123456789abcdef",
		CookieName:    "audit_admin_session",
		Now: func() time.Time {
			return time.Unix(1000, 0).UTC()
		},
	}
	handler := NewHandler(HandlerConfig{Repo: repo, Auth: auth})

	loginBody := bytes.NewBufferString(`{"username":"alice","password":"secret-password"}`)
	loginReq := httptest.NewRequest(http.MethodPost, "/admin/api/login", loginBody)
	loginRec := httptest.NewRecorder()
	handler.ServeHTTP(loginRec, loginReq)

	if loginRec.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", loginRec.Code, loginRec.Body.String())
	}
	if strings.Contains(loginRec.Body.String(), passwordHash) {
		t.Fatal("login response leaked password hash")
	}
	cookies := loginRec.Result().Cookies()
	var sessionCookie *http.Cookie
	var csrfCookie *http.Cookie
	for _, cookie := range cookies {
		switch cookie.Name {
		case "audit_admin_session":
			sessionCookie = cookie
		case "audit_admin_csrf":
			csrfCookie = cookie
		}
	}
	if sessionCookie == nil || csrfCookie == nil {
		t.Fatalf("cookies = %#v", cookies)
	}

	meReq := httptest.NewRequest(http.MethodGet, "/admin/api/me", nil)
	meReq.AddCookie(sessionCookie)
	meRec := httptest.NewRecorder()
	handler.ServeHTTP(meRec, meReq)

	if meRec.Code != http.StatusOK {
		t.Fatalf("me status = %d, body = %s", meRec.Code, meRec.Body.String())
	}
	var body struct {
		User Principal `json:"user"`
	}
	if err := json.Unmarshal(meRec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode me body: %v", err)
	}
	if body.User.Username != "alice" || body.User.Role != RoleAuditor {
		t.Fatalf("me body = %#v", body)
	}
	if strings.Contains(meRec.Body.String(), passwordHash) {
		t.Fatal("me response leaked password hash")
	}

	logoutReq := httptest.NewRequest(http.MethodPost, "/admin/api/logout", nil)
	logoutReq.AddCookie(sessionCookie)
	logoutRec := httptest.NewRecorder()
	handler.ServeHTTP(logoutRec, logoutReq)
	if logoutRec.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d", logoutRec.Code)
	}
	if db.revokedSessionID == "" {
		t.Fatal("session was not revoked")
	}
}

func TestChangeCurrentUserPasswordSucceedsAndRevokesOtherSessions(t *testing.T) {
	handler, db, cookie := newAuthenticatedAdminHandler(t, RoleViewer, "", nil)
	oldHash := db.user.PasswordHash
	req := httptest.NewRequest(http.MethodPost, "/admin/api/me/password", strings.NewReader(`{"current_password":"secret-password","new_password":"new-secret-password","confirm_password":"new-secret-password"}`))
	addAuthenticatedCookies(req, cookie)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if db.updatedPasswordUserID != int64(1) {
		t.Fatalf("updatedPasswordUserID = %d, want 1", db.updatedPasswordUserID)
	}
	if db.updatedPasswordHash == "" || db.updatedPasswordHash == oldHash {
		t.Fatalf("updatedPasswordHash was not changed")
	}
	if err := CheckPassword(db.updatedPasswordHash, "new-secret-password"); err != nil {
		t.Fatalf("new password does not verify: %v", err)
	}
	if err := CheckPassword(db.updatedPasswordHash, "secret-password"); err == nil {
		t.Fatal("old password still verifies")
	}
	if db.revokedOtherUserID != int64(1) {
		t.Fatalf("revokedOtherUserID = %d, want 1", db.revokedOtherUserID)
	}
	if db.revokedOtherKeepSession == "" {
		t.Fatal("current session id was not passed to RevokeOtherSessions")
	}
	if db.revokedOtherAt != time.Unix(1000, 0).UTC() {
		t.Fatalf("revokedOtherAt = %s, want auth now", db.revokedOtherAt)
	}
	if len(db.auditActions) == 0 || db.auditActions[len(db.auditActions)-1] != "password_changed" {
		t.Fatalf("auditActions = %#v", db.auditActions)
	}
	if len(db.auditMetadata) == 0 || db.auditMetadata[len(db.auditMetadata)-1] != `{"revoked_other_sessions":true}` {
		t.Fatalf("auditMetadata = %#v", db.auditMetadata)
	}
	if got := strings.Join(db.passwordChangeOps, ","); got != "revoke_other_sessions,update_password,audit_log" {
		t.Fatalf("passwordChangeOps = %s, want revoke_other_sessions,update_password,audit_log", got)
	}

	meReq := httptest.NewRequest(http.MethodGet, "/admin/api/me", nil)
	meReq.AddCookie(cookie)
	meRec := httptest.NewRecorder()
	handler.ServeHTTP(meRec, meReq)
	if meRec.Code != http.StatusOK {
		t.Fatalf("me status after password change = %d, body = %s", meRec.Code, meRec.Body.String())
	}
}

func TestChangeCurrentUserPasswordPreservesNewPasswordSpaces(t *testing.T) {
	handler, db, cookie := newAuthenticatedAdminHandler(t, RoleViewer, "", nil)
	req := httptest.NewRequest(http.MethodPost, "/admin/api/me/password", strings.NewReader(`{"current_password":"secret-password","new_password":" new-secret-password ","confirm_password":" new-secret-password "}`))
	addAuthenticatedCookies(req, cookie)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if err := CheckPassword(db.updatedPasswordHash, " new-secret-password "); err != nil {
		t.Fatalf("new password with spaces does not verify: %v", err)
	}
	if err := CheckPassword(db.updatedPasswordHash, "new-secret-password"); err == nil {
		t.Fatal("trimmed new password verifies")
	}
}

func TestChangeCurrentUserPasswordRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		wantCode int
		wantText string
	}{
		{
			name:     "wrong current password",
			body:     `{"current_password":"wrong-password","new_password":"new-secret-password","confirm_password":"new-secret-password"}`,
			wantCode: http.StatusUnauthorized,
			wantText: "current password is incorrect",
		},
		{
			name:     "current password with extra spaces",
			body:     `{"current_password":" secret-password ","new_password":"new-secret-password","confirm_password":"new-secret-password"}`,
			wantCode: http.StatusUnauthorized,
			wantText: "current password is incorrect",
		},
		{
			name:     "short new password",
			body:     `{"current_password":"secret-password","new_password":"short","confirm_password":"short"}`,
			wantCode: http.StatusBadRequest,
			wantText: "new password must be at least 12 characters",
		},
		{
			name:     "confirmation mismatch",
			body:     `{"current_password":"secret-password","new_password":"new-secret-password","confirm_password":"different-password"}`,
			wantCode: http.StatusBadRequest,
			wantText: "new password confirmation does not match",
		},
		{
			name:     "same as current password",
			body:     `{"current_password":"secret-password","new_password":"secret-password","confirm_password":"secret-password"}`,
			wantCode: http.StatusBadRequest,
			wantText: "new password must be different from current password",
		},
		{
			name:     "missing field",
			body:     `{"current_password":"secret-password","new_password":"new-secret-password"}`,
			wantCode: http.StatusBadRequest,
			wantText: "password fields are required",
		},
		{
			name:     "invalid json",
			body:     `{"current_password":"secret-password"`,
			wantCode: http.StatusBadRequest,
			wantText: "invalid json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler, db, cookie := newAuthenticatedAdminHandler(t, RoleAuditor, "", nil)
			oldHash := db.user.PasswordHash
			req := httptest.NewRequest(http.MethodPost, "/admin/api/me/password", strings.NewReader(tt.body))
			addAuthenticatedCookies(req, cookie)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d, body = %s", rec.Code, tt.wantCode, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tt.wantText) {
				t.Fatalf("body = %q, want %q", rec.Body.String(), tt.wantText)
			}
			if db.user.PasswordHash != oldHash {
				t.Fatal("password hash changed after invalid request")
			}
			if db.revokedOtherUserID != 0 {
				t.Fatalf("revokedOtherUserID = %d, want 0", db.revokedOtherUserID)
			}
		})
	}
}

func TestChangeCurrentUserPasswordRevokeFailureDoesNotUpdatePassword(t *testing.T) {
	handler, db, cookie := newAuthenticatedAdminHandler(t, RoleAuditor, "", nil)
	oldHash := db.user.PasswordHash
	db.revokeOtherErr = errors.New("revoke failed")
	req := httptest.NewRequest(http.MethodPost, "/admin/api/me/password", strings.NewReader(`{"current_password":"secret-password","new_password":"new-secret-password","confirm_password":"new-secret-password"}`))
	addAuthenticatedCookies(req, cookie)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500, body = %s", rec.Code, rec.Body.String())
	}
	if db.user.PasswordHash != oldHash {
		t.Fatal("password hash changed despite session revocation failure")
	}
	if db.updatedPasswordUserID != 0 {
		t.Fatalf("updatedPasswordUserID = %d, want 0", db.updatedPasswordUserID)
	}
	if len(db.auditActions) != 1 || db.auditActions[0] != "login" {
		t.Fatalf("auditActions = %#v, want only login audit", db.auditActions)
	}
	if got := strings.Join(db.passwordChangeOps, ","); got != "" {
		t.Fatalf("passwordChangeOps = %s, want no committed password change ops", got)
	}
}

func TestChangeCurrentUserPasswordUpdateFailureKeepsRevocationVisible(t *testing.T) {
	handler, db, cookie := newAuthenticatedAdminHandler(t, RoleAuditor, "", nil)
	oldHash := db.user.PasswordHash
	db.updatePasswordErr = errors.New("update failed")
	req := httptest.NewRequest(http.MethodPost, "/admin/api/me/password", strings.NewReader(`{"current_password":"secret-password","new_password":"new-secret-password","confirm_password":"new-secret-password"}`))
	addAuthenticatedCookies(req, cookie)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500, body = %s", rec.Code, rec.Body.String())
	}
	if db.revokedOtherUserID != 0 {
		t.Fatalf("revokedOtherUserID = %d, want 0", db.revokedOtherUserID)
	}
	if db.user.PasswordHash != oldHash {
		t.Fatal("password hash changed despite update failure")
	}
	if len(db.auditActions) != 1 || db.auditActions[0] != "login" {
		t.Fatalf("auditActions = %#v, want only login audit", db.auditActions)
	}
	if got := strings.Join(db.passwordChangeOps, ","); got != "" {
		t.Fatalf("passwordChangeOps = %s, want no committed password change ops", got)
	}
}

func TestChangeCurrentUserPasswordAuditFailureRollsBackPasswordChange(t *testing.T) {
	handler, db, cookie := newAuthenticatedAdminHandler(t, RoleAuditor, "", nil)
	oldHash := db.user.PasswordHash
	db.auditErr = errors.New("audit failed")
	req := httptest.NewRequest(http.MethodPost, "/admin/api/me/password", strings.NewReader(`{"current_password":"secret-password","new_password":"new-secret-password","confirm_password":"new-secret-password"}`))
	addAuthenticatedCookies(req, cookie)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500, body = %s", rec.Code, rec.Body.String())
	}
	if db.revokedOtherUserID != 0 {
		t.Fatalf("revokedOtherUserID = %d, want 0", db.revokedOtherUserID)
	}
	if db.user.PasswordHash != oldHash {
		t.Fatal("password hash changed despite audit failure")
	}
	if db.updatedPasswordUserID != 0 {
		t.Fatalf("updatedPasswordUserID = %d, want 0", db.updatedPasswordUserID)
	}
	if len(db.auditActions) != 1 || db.auditActions[0] != "login" {
		t.Fatalf("auditActions = %#v, want only login audit", db.auditActions)
	}
	if got := strings.Join(db.passwordChangeOps, ","); got != "" {
		t.Fatalf("passwordChangeOps = %s, want no committed password change ops", got)
	}
}

func TestChangeCurrentUserPasswordRequiresCSRF(t *testing.T) {
	handler, _, cookie := newAuthenticatedAdminHandler(t, RoleAuditor, "", nil)
	req := httptest.NewRequest(http.MethodPost, "/admin/api/me/password", strings.NewReader(`{"current_password":"secret-password","new_password":"new-secret-password","confirm_password":"new-secret-password"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", "forged-csrf")
	req.AddCookie(&http.Cookie{Name: "audit_admin_csrf", Value: "forged-csrf"})
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestLogoutClearsMalformedAndStaleCookies(t *testing.T) {
	db := &memoryAdminDB{
		user: User{ID: 1, Username: "alice", DisplayName: "Alice", Role: RoleAuditor, Status: "active"},
	}
	repo := NewRepository(db)
	auth := Auth{
		Repo:          repo,
		SessionSecret: "session-secret-0123456789abcdef",
		CookieName:    "audit_admin_session",
		Now: func() time.Time {
			return time.Unix(1000, 0).UTC()
		},
	}
	handler := NewHandler(HandlerConfig{Repo: repo, Auth: auth})

	tests := []struct {
		name   string
		cookie *http.Cookie
	}{
		{
			name:   "malformed",
			cookie: &http.Cookie{Name: "audit_admin_session", Value: "not-a-signed-cookie"},
		},
		{
			name:   "stale",
			cookie: auth.sessionCookie("sess_stale", time.Unix(2000, 0).UTC()),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/admin/api/logout", nil)
			req.AddCookie(tt.cookie)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusNoContent {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			assertClearSessionCookie(t, rec.Result().Cookies())
		})
	}
}

func TestLogoutReportsRevocationFailureAndClearsCookie(t *testing.T) {
	passwordHash, err := HashPassword("secret-password")
	if err != nil {
		t.Fatalf("HashPassword error: %v", err)
	}
	db := &memoryAdminDB{
		user: User{ID: 1, Username: "alice", PasswordHash: passwordHash, DisplayName: "Alice", Role: RoleAuditor, Status: "active"},
	}
	repo := NewRepository(db)
	auth := Auth{
		Repo:          repo,
		SessionSecret: "session-secret-0123456789abcdef",
		CookieName:    "audit_admin_session",
		Now: func() time.Time {
			return time.Unix(1000, 0).UTC()
		},
	}
	handler := NewHandler(HandlerConfig{Repo: repo, Auth: auth})

	loginReq := httptest.NewRequest(http.MethodPost, "/admin/api/login", bytes.NewBufferString(`{"username":"alice","password":"secret-password"}`))
	loginRec := httptest.NewRecorder()
	handler.ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", loginRec.Code, loginRec.Body.String())
	}
	cookie := loginRec.Result().Cookies()[0]

	db.revokeErr = errors.New("database unavailable")
	logoutReq := httptest.NewRequest(http.MethodPost, "/admin/api/logout", nil)
	logoutReq.AddCookie(cookie)
	logoutRec := httptest.NewRecorder()
	handler.ServeHTTP(logoutRec, logoutReq)

	if logoutRec.Code != http.StatusInternalServerError {
		t.Fatalf("logout status = %d, want 500", logoutRec.Code)
	}
	assertClearSessionCookie(t, logoutRec.Result().Cookies())
}

func TestLoginDistinguishesCredentialAndRepositoryFailures(t *testing.T) {
	passwordHash, err := HashPassword("secret-password")
	if err != nil {
		t.Fatalf("HashPassword error: %v", err)
	}

	tests := []struct {
		name       string
		db         *memoryAdminDB
		body       string
		wantStatus int
	}{
		{
			name:       "not found",
			db:         &memoryAdminDB{user: User{ID: 1, Username: "alice", PasswordHash: passwordHash, Role: RoleAuditor, Status: "active"}},
			body:       `{"username":"missing","password":"secret-password"}`,
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "bad password",
			db:         &memoryAdminDB{user: User{ID: 1, Username: "alice", PasswordHash: passwordHash, Role: RoleAuditor, Status: "active"}},
			body:       `{"username":"alice","password":"wrong-password"}`,
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "repository failure",
			db:         &memoryAdminDB{findUserErr: errors.New("database unavailable")},
			body:       `{"username":"alice","password":"secret-password"}`,
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := NewRepository(tt.db)
			auth := Auth{
				Repo:          repo,
				SessionSecret: "session-secret-0123456789abcdef",
				CookieName:    "audit_admin_session",
			}
			handler := NewHandler(HandlerConfig{Repo: repo, Auth: auth})

			req := httptest.NewRequest(http.MethodPost, "/admin/api/login", bytes.NewBufferString(tt.body))
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d, body = %s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "secret-password") || strings.Contains(rec.Body.String(), passwordHash) {
				t.Fatalf("response leaked credential material: %q", rec.Body.String())
			}
		})
	}
}

func TestViewerCannotCreateReviewDecision(t *testing.T) {
	db := &memoryAdminDB{
		user: User{ID: 2, Username: "viewer", PasswordHash: "$2a$10$012345678901234567890uRZMFv4I2rGgbJ5h1x3zsmYqzqzqzqzq", DisplayName: "Viewer", Role: RoleViewer, Status: "active"},
	}
	repo := NewRepository(db)
	auth := Auth{
		Repo:          repo,
		SessionSecret: "session-secret-0123456789abcdef",
		CookieName:    "audit_admin_session",
		Now: func() time.Time {
			return time.Unix(1000, 0).UTC()
		},
	}
	handler := NewHandler(HandlerConfig{Repo: repo, Auth: auth})
	req := httptest.NewRequest(http.MethodPost, "/admin/api/reviews", bytes.NewBufferString(`{"target_type":"anomaly","target_id":"anom_1","decision":"acknowledge","note":"seen"}`))
	req = req.WithContext(WithPrincipal(req.Context(), Principal{UserID: 2, Username: "viewer", Role: RoleViewer}))
	rec := httptest.NewRecorder()

	handler.auth.Require(PermissionReview, http.HandlerFunc(handler.createReview)).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestCreateReviewRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "invalid target type",
			body: `{"target_type":"user","target_id":"anom_1","decision":"acknowledge","note":"seen"}`,
		},
		{
			name: "invalid decision",
			body: `{"target_type":"anomaly","target_id":"anom_1","decision":"approve","note":"seen"}`,
		},
		{
			name: "missing target id",
			body: `{"target_type":"anomaly","target_id":"   ","decision":"acknowledge","note":"seen"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler, db, cookie := newAuthenticatedReviewHandler(t)
			req := httptest.NewRequest(http.MethodPost, "/admin/api/reviews", bytes.NewBufferString(tt.body))
			addAuthenticatedCookies(req, cookie)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
			}
			if len(db.reviewDecisions) != 0 {
				t.Fatalf("review decisions inserted for invalid input: %#v", db.reviewDecisions)
			}
		})
	}
}

func TestCreateReviewWritesValidAuditMetadata(t *testing.T) {
	handler, db, cookie := newAuthenticatedReviewHandler(t)
	db.auditActions = nil
	db.auditMetadata = nil
	req := httptest.NewRequest(http.MethodPost, "/admin/api/reviews", bytes.NewBufferString(`{"target_type":"anomaly","target_id":"anom_1","decision":"acknowledge","note":"seen"}`))
	addAuthenticatedCookies(req, cookie)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body = %s", rec.Code, rec.Body.String())
	}
	if len(db.reviewDecisions) != 1 {
		t.Fatalf("review decisions = %#v, want one insert", db.reviewDecisions)
	}
	if len(db.auditMetadata) != 1 {
		t.Fatalf("audit metadata = %#v, want one entry", db.auditMetadata)
	}
	var metadata map[string]string
	if err := json.Unmarshal([]byte(db.auditMetadata[0]), &metadata); err != nil {
		t.Fatalf("audit metadata is not valid JSON: %v", err)
	}
	if metadata["decision"] != "acknowledge" {
		t.Fatalf("audit metadata = %#v", metadata)
	}
}

func TestAPIKeyLookupDoesNotPersistPlaintextKeyInAuditLog(t *testing.T) {
	const plaintextKey = "sk-secret-plain-text"
	const auditSecret = "audit-secret-0123456789abcdef"
	handler, db, cookie := newAuthenticatedAdminHandler(t, RoleRawAccess, auditSecret, nil)
	db.auditLogs = nil
	db.auditActions = nil
	db.auditMetadata = nil
	wantFingerprint := fingerprint.Compute("secret", auditSecret)

	req := httptest.NewRequest(http.MethodPost, "/admin/api/api-key-lookup", bytes.NewBufferString(`{"api_key":"`+plaintextKey+`"}`))
	addAuthenticatedCookies(req, cookie)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if strings.Contains(rec.Body.String(), plaintextKey) {
		t.Fatal("lookup response leaked plaintext key")
	}
	if db.lookupTokenFingerprint != wantFingerprint.Value {
		t.Fatalf("lookup fingerprint = %q, want computed fingerprint", db.lookupTokenFingerprint)
	}
	if !strings.Contains(rec.Body.String(), wantFingerprint.Display) {
		t.Fatalf("lookup response did not include computed display fingerprint")
	}
	if len(db.auditLogs) != 1 {
		t.Fatalf("audit logs = %#v, want one lookup audit log", db.auditLogs)
	}
	log := db.auditLogs[0]
	if log.Action != "api_key_lookup" || log.TargetID != wantFingerprint.Display {
		t.Fatalf("audit log = %#v", log)
	}
	auditText := strings.Join([]string{
		log.TargetID,
		log.TokenFingerprint,
		log.FingerprintDisplay,
		log.TraceID,
		log.MetadataJSON,
		strings.Join(db.auditMetadata, " "),
	}, " ")
	if strings.Contains(auditText, plaintextKey) {
		t.Fatal("audit log persisted plaintext key")
	}
}

func TestUnsafeAdminRequestRequiresCSRFToken(t *testing.T) {
	h, _, cookie := newAuthenticatedAdminHandler(t, RoleRawAccess, "audit-secret-0123456789abcdef", nil)

	req := httptest.NewRequest(http.MethodPost, "/admin/api/api-key-lookup", strings.NewReader(`{"api_key":"sk-secret-extra"}`))
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestAPIKeyLookupRateLimit(t *testing.T) {
	h, _, cookie := newAuthenticatedAdminHandler(t, RoleRawAccess, "audit-secret-0123456789abcdef", nil)
	h.lookupLimiter = NewMemoryRateLimiter(1, time.Hour)

	for attempt := 0; attempt < 2; attempt++ {
		req := httptest.NewRequest(http.MethodPost, "/admin/api/api-key-lookup", strings.NewReader(`{"api_key":"sk-secret-extra"}`))
		addAuthenticatedCookies(req, cookie)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if attempt == 1 && rec.Code != http.StatusTooManyRequests {
			t.Fatalf("second status = %d, want 429", rec.Code)
		}
	}
}

func TestUnsafeAdminRequestRejectsSelfConsistentForgedCSRFToken(t *testing.T) {
	h, _, cookie := newAuthenticatedAdminHandler(t, RoleRawAccess, "audit-secret-0123456789abcdef", nil)

	req := httptest.NewRequest(http.MethodPost, "/admin/api/api-key-lookup", strings.NewReader(`{"api_key":"sk-secret-extra"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", "forged-csrf")
	req.AddCookie(&http.Cookie{Name: "audit_admin_csrf", Value: "forged-csrf"})
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestRawEvidenceAccessStreamsObjectAndWritesAuditLog(t *testing.T) {
	handler, db, cookie := newAuthenticatedAdminHandler(t, RoleRawAccess, "audit-secret-0123456789abcdef", fakeEvidenceStore{
		body: "raw evidence bytes",
	})
	db.rawEvidenceObject = EvidenceObjectSummary{
		TraceID:     "trace_123",
		ObjectType:  "request_body",
		ObjectRef:   "raw/trace_123/request_body.bin",
		ContentType: "application/json",
		SizeBytes:   18,
		SHA256:      "abc123",
	}
	db.auditLogs = nil
	db.auditActions = nil
	db.auditMetadata = nil

	req := httptest.NewRequest(http.MethodGet, "/admin/api/raw-evidence/trace_123/request_body", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "raw evidence bytes" {
		t.Fatalf("body = %q", rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("content type = %q", got)
	}
	if got := rec.Header().Get("X-Audit-Evidence-SHA256"); got != "abc123" {
		t.Fatalf("sha header = %q", got)
	}
	if len(db.auditLogs) != 1 {
		t.Fatalf("audit logs = %#v, want one raw evidence audit log", db.auditLogs)
	}
	log := db.auditLogs[0]
	if log.Action != "raw_evidence_access" || log.TraceID != "trace_123" || log.TargetID != "request_body" {
		t.Fatalf("audit log = %#v", log)
	}
}

func TestRawEvidenceAccessRateLimit(t *testing.T) {
	handler, db, cookie := newAuthenticatedAdminHandler(t, RoleRawAccess, "audit-secret-0123456789abcdef", fakeEvidenceStore{
		body: "raw evidence bytes",
	})
	handler.rawLimiter = NewMemoryRateLimiter(1, time.Hour)
	db.rawEvidenceObject = EvidenceObjectSummary{
		TraceID:     "trace_123",
		ObjectType:  "request_body",
		ObjectRef:   "raw/trace_123/request_body.bin",
		ContentType: "application/json",
		SizeBytes:   18,
		SHA256:      "abc123",
	}

	for attempt := 0; attempt < 2; attempt++ {
		req := httptest.NewRequest(http.MethodGet, "/admin/api/raw-evidence/trace_123/request_body", nil)
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if attempt == 0 && rec.Code != http.StatusOK {
			t.Fatalf("first status = %d, want 200, body = %s", rec.Code, rec.Body.String())
		}
		if attempt == 1 && rec.Code != http.StatusTooManyRequests {
			t.Fatalf("second status = %d, want 429", rec.Code)
		}
	}
}

func TestRawEvidenceAccessSelectsObjectRefAndAuditsMetadata(t *testing.T) {
	store := &recordingEvidenceStore{
		bodies: map[string]string{
			"raw/2026/04/30/trace_123/multipart_part_000001.bin": "first part",
		},
	}
	handler, db, cookie := newAuthenticatedAdminHandler(t, RoleRawAccess, "audit-secret-0123456789abcdef", store)
	db.rawEvidenceObject = EvidenceObjectSummary{
		TraceID:     "trace_123",
		ObjectType:  "multipart_part",
		ObjectRef:   "raw/2026/04/30/trace_123/multipart_part_000001.bin",
		ContentType: "application/octet-stream",
		SizeBytes:   10,
		SHA256:      "part-sha",
	}
	db.auditLogs = nil
	db.auditActions = nil
	db.auditMetadata = nil

	req := httptest.NewRequest(http.MethodGet, "/admin/api/raw-evidence/trace_123/multipart_part?object_ref=raw/2026/04/30/trace_123/multipart_part_000001.bin", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "first part" {
		t.Fatalf("body = %q", rec.Body.String())
	}
	if store.requestedRef != "raw/2026/04/30/trace_123/multipart_part_000001.bin" {
		t.Fatalf("requested evidence ref = %q", store.requestedRef)
	}
	if !strings.Contains(db.rawEvidenceSQL, "object_ref = $3") {
		t.Fatalf("raw evidence sql = %q, want object_ref filter", db.rawEvidenceSQL)
	}
	if len(db.rawEvidenceArgs) != 3 || db.rawEvidenceArgs[2] != "raw/2026/04/30/trace_123/multipart_part_000001.bin" {
		t.Fatalf("raw evidence args = %#v", db.rawEvidenceArgs)
	}
	if len(db.auditMetadata) != 1 || !strings.Contains(db.auditMetadata[0], `"object_ref":"raw/2026/04/30/trace_123/multipart_part_000001.bin"`) {
		t.Fatalf("audit metadata = %#v", db.auditMetadata)
	}
	if strings.Contains(db.auditMetadata[0], "first part") {
		t.Fatalf("audit metadata leaked body: %s", db.auditMetadata[0])
	}
}

func TestTraceDetailRedactsRawRefsForAuditor(t *testing.T) {
	handler, db, cookie := newAuthenticatedAdminHandler(t, RoleAuditor, "audit-secret-0123456789abcdef", nil)
	db.traceDetail = traceDetailWithRawRefs()

	req := httptest.NewRequest(http.MethodGet, "/admin/api/traces/trace_123", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Trace TraceDetail `json:"trace"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode trace detail body: %v", err)
	}
	if body.Trace.RequestRawRef != "" || body.Trace.ResponseRawRef != "" || body.Trace.RequestHeadersRef != "" || body.Trace.ResponseHeadersRef != "" {
		t.Fatalf("raw refs were not redacted: %#v", body.Trace)
	}
	for _, rawRef := range rawRefValues() {
		if strings.Contains(rec.Body.String(), rawRef) {
			t.Fatalf("response leaked raw ref %q: %s", rawRef, rec.Body.String())
		}
	}
}

func TestTraceDetailIncludesRawRefsForRawEvidenceRoles(t *testing.T) {
	for _, role := range []Role{RoleRawAccess, RoleAdmin} {
		t.Run(string(role), func(t *testing.T) {
			handler, db, cookie := newAuthenticatedAdminHandler(t, role, "audit-secret-0123456789abcdef", nil)
			db.traceDetail = traceDetailWithRawRefs()

			req := httptest.NewRequest(http.MethodGet, "/admin/api/traces/trace_123", nil)
			req.AddCookie(cookie)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
			}
			var body struct {
				Trace TraceDetail `json:"trace"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode trace detail body: %v", err)
			}
			if body.Trace.UsagePromptTokens != 12 || body.Trace.UsageCompletionTokens != 23 ||
				body.Trace.UsageCachedTokens != 7 || body.Trace.UsageTotalTokens != 42 {
				t.Fatalf("usage fields = %#v", body.Trace.TraceSummary)
			}
			if body.Trace.RequestRawRef != "raw/trace_123/request_body.bin" ||
				body.Trace.ResponseRawRef != "raw/trace_123/response_body.bin" ||
				body.Trace.RequestHeadersRef != "raw/trace_123/request_headers.json" ||
				body.Trace.ResponseHeadersRef != "raw/trace_123/response_headers.json" {
				t.Fatalf("raw refs = %#v", body.Trace)
			}
		})
	}
}

func TestRawEvidenceAccessRequiresRawEvidencePermission(t *testing.T) {
	handler, db, cookie := newAuthenticatedAdminHandler(t, RoleAuditor, "audit-secret-0123456789abcdef", fakeEvidenceStore{
		body: "raw evidence bytes",
	})
	db.rawEvidenceObject = EvidenceObjectSummary{
		TraceID:     "trace_123",
		ObjectType:  "request_body",
		ObjectRef:   "raw/trace_123/request_body.bin",
		ContentType: "application/json",
		SizeBytes:   18,
		SHA256:      "abc123",
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/api/raw-evidence/trace_123/request_body", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403, body = %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "raw evidence bytes") {
		t.Fatal("raw evidence bytes were streamed without raw evidence permission")
	}
}

func TestRawEvidenceAccessWithoutStoreReturnsUnavailable(t *testing.T) {
	handler, db, cookie := newAuthenticatedAdminHandler(t, RoleRawAccess, "audit-secret-0123456789abcdef", nil)
	db.rawEvidenceObject = EvidenceObjectSummary{
		TraceID:     "trace_123",
		ObjectType:  "request_body",
		ObjectRef:   "raw/trace_123/request_body.bin",
		ContentType: "application/json",
		SHA256:      "abc123",
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/api/raw-evidence/trace_123/request_body", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

func TestRawEvidenceAccessLookupErrorReturnsNotFound(t *testing.T) {
	handler, db, cookie := newAuthenticatedAdminHandler(t, RoleRawAccess, "audit-secret-0123456789abcdef", fakeEvidenceStore{
		body: "raw evidence bytes",
	})
	db.rawEvidenceErr = errors.New("lookup failed")

	req := httptest.NewRequest(http.MethodGet, "/admin/api/raw-evidence/trace_123/request_body", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestRawEvidenceAccessAuditFailureDoesNotStreamObject(t *testing.T) {
	handler, db, cookie := newAuthenticatedAdminHandler(t, RoleRawAccess, "audit-secret-0123456789abcdef", fakeEvidenceStore{
		body: "raw evidence bytes",
	})
	db.rawEvidenceObject = EvidenceObjectSummary{
		TraceID:     "trace_123",
		ObjectType:  "request_body",
		ObjectRef:   "raw/trace_123/request_body.bin",
		ContentType: "application/json",
		SHA256:      "abc123",
	}
	db.auditErr = errors.New("audit insert failed")

	req := httptest.NewRequest(http.MethodGet, "/admin/api/raw-evidence/trace_123/request_body", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "raw evidence bytes") {
		t.Fatal("raw evidence bytes were streamed despite audit failure")
	}
}

func TestOverviewRequiresAggregatePermission(t *testing.T) {
	handler, _, cookie := newAuthenticatedAdminHandler(t, RoleViewer, "", nil)
	req := httptest.NewRequest(http.MethodGet, "/admin/api/overview", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
}

func TestUsageWithUsernameReturnsEmployeeUsage(t *testing.T) {
	now := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)
	handler, db, cookie := newAuthenticatedAdminHandler(t, RoleViewer, "", nil)
	handler.auth.Now = func() time.Time { return now }
	db.session.ExpiresAt = now.Add(time.Hour)
	req := httptest.NewRequest(http.MethodGet, "/admin/api/usage?username=E10001&range=bad&model=%20gpt-5.2%20", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	if db.usageModelFilter != "gpt-5.2" {
		t.Fatalf("usage model filter = %q, want trimmed gpt-5.2", db.usageModelFilter)
	}
	if db.employeeUsageFilter.Username != "E10001" || db.employeeUsageFilter.Model != "gpt-5.2" {
		t.Fatalf("filter = %#v", db.employeeUsageFilter)
	}
	if !db.employeeUsageFilter.End.Equal(now) {
		t.Fatalf("end = %s, want %s", db.employeeUsageFilter.End, now)
	}
	if !db.employeeUsageFilter.Start.Equal(now.AddDate(0, 0, -30)) {
		t.Fatalf("start = %s, want %s", db.employeeUsageFilter.Start, now.AddDate(0, 0, -30))
	}
	var body struct {
		EmployeeUsage EmployeeUsageTrend `json:"employee_usage"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.EmployeeUsage.Username != "E10001" || body.EmployeeUsage.Summary.TotalTokens != 15 {
		t.Fatalf("employee_usage = %#v", body.EmployeeUsage)
	}
	if body.EmployeeUsage.Range != "30d" || body.EmployeeUsage.SelectedModel != "gpt-5.2" {
		t.Fatalf("employee_usage range/model = %#v", body.EmployeeUsage)
	}
}

func TestUsageWithoutUsernameKeepsGenericResponse(t *testing.T) {
	handler, db, cookie := newAuthenticatedAdminHandler(t, RoleViewer, "", nil)
	req := httptest.NewRequest(http.MethodGet, "/admin/api/usage", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	if db.employeeUsageCalled {
		t.Fatal("employee usage trend was called without username")
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if _, ok := body["usage"]; !ok {
		t.Fatalf("body missing usage: %s", rec.Body.String())
	}
	if _, ok := body["employee_usage"]; ok {
		t.Fatalf("body unexpectedly included employee_usage: %s", rec.Body.String())
	}
}

func TestOverviewReturnsThirtyDayTokenUsage(t *testing.T) {
	handler, _, cookie := newAuthenticatedAdminHandler(t, RoleViewer, "", nil)
	req := httptest.NewRequest(http.MethodGet, "/admin/api/overview", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Overview OverviewSummary `json:"overview"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode overview body: %v", err)
	}
	if len(body.Overview.TokenUsageDaily) != 30 {
		t.Fatalf("token_usage_daily length = %d, want 30; body = %s", len(body.Overview.TokenUsageDaily), rec.Body.String())
	}
}

func TestProductCompletionRoutesReturnJSON(t *testing.T) {
	tests := []struct {
		name    string
		role    Role
		path    string
		wantKey string
	}{
		{name: "token identities", role: RoleViewer, path: "/admin/api/token-identities", wantKey: "token_identities"},
		{name: "review decisions", role: RoleAuditor, path: "/admin/api/review-decisions", wantKey: "review_decisions"},
		{name: "settings", role: RoleAdmin, path: "/admin/api/settings", wantKey: "settings"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler, _, cookie := newAuthenticatedAdminHandler(t, tt.role, "", nil)
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			req.AddCookie(cookie)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
			}
			var body map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if _, ok := body[tt.wantKey]; !ok {
				t.Fatalf("body missing %q: %s", tt.wantKey, rec.Body.String())
			}
		})
	}
}

func TestContextCatalogCreateRequiresReviewPermissionAndWritesActor(t *testing.T) {
	handler, db, cookie := newAuthenticatedAdminHandler(t, RoleAuditor, "", nil)
	req := httptest.NewRequest(http.MethodPost, "/admin/api/context-catalog", bytes.NewBufferString(`{
		"context_type":"repo",
		"name":"new-api-gateway",
		"description":"Audit gateway repository",
		"keywords":["gateway","new-api"],
		"aliases":["audit gateway"],
		"owner":"platform",
		"expected_task_categories":["coding","debugging"],
		"expected_models":["gpt-5.2"],
		"expected_usage_level":"medium",
		"active":true
	}`))
	addAuthenticatedCookies(req, cookie)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body = %s", rec.Code, rec.Body.String())
	}
	if db.contextEntry.Name != "new-api-gateway" || db.contextEntry.CreatedBy != "alice" || db.contextEntry.UpdatedBy != "alice" {
		t.Fatalf("context entry = %#v", db.contextEntry)
	}
}

func TestContextCatalogCreateDefaultsOmittedActiveToTrue(t *testing.T) {
	handler, db, cookie := newAuthenticatedAdminHandler(t, RoleAuditor, "", nil)
	req := httptest.NewRequest(http.MethodPost, "/admin/api/context-catalog", bytes.NewBufferString(`{
		"context_type":"repo",
		"name":"default-active",
		"expected_usage_level":"low"
	}`))
	addAuthenticatedCookies(req, cookie)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body = %s", rec.Code, rec.Body.String())
	}
	if !db.contextEntry.Active {
		t.Fatalf("active = false, want true for omitted active")
	}
}

func TestContextCatalogCreatePersistsExplicitActiveFalse(t *testing.T) {
	handler, db, cookie := newAuthenticatedAdminHandler(t, RoleAuditor, "", nil)
	req := httptest.NewRequest(http.MethodPost, "/admin/api/context-catalog", bytes.NewBufferString(`{
		"context_type":"repo",
		"name":"inactive-repo",
		"expected_usage_level":"low",
		"active":false
	}`))
	addAuthenticatedCookies(req, cookie)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body = %s", rec.Code, rec.Body.String())
	}
	if db.contextEntry.Active {
		t.Fatalf("active = true, want false for explicit active false")
	}
}

func TestContextCatalogCreateAuditFailureReturnsErrorAfterUpsert(t *testing.T) {
	handler, db, cookie := newAuthenticatedAdminHandler(t, RoleAuditor, "", nil)
	db.auditErr = errors.New("audit insert failed")
	req := httptest.NewRequest(http.MethodPost, "/admin/api/context-catalog", bytes.NewBufferString(`{
		"context_type":"repo",
		"name":"audit-failure",
		"expected_usage_level":"medium"
	}`))
	addAuthenticatedCookies(req, cookie)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500, body = %s", rec.Code, rec.Body.String())
	}
	if db.contextEntry.Name != "audit-failure" {
		t.Fatalf("context entry was not upserted before audit failure: %#v", db.contextEntry)
	}
}

func TestContextCatalogCreateRejectsInvalidInput(t *testing.T) {
	handler, db, cookie := newAuthenticatedAdminHandler(t, RoleAuditor, "", nil)
	req := httptest.NewRequest(http.MethodPost, "/admin/api/context-catalog", bytes.NewBufferString(`{
		"context_type":"unknown",
		"name":"invalid-context"
	}`))
	addAuthenticatedCookies(req, cookie)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
	}
	if db.contextEntry.Name != "" {
		t.Fatalf("context entry inserted for invalid input: %#v", db.contextEntry)
	}
}

func TestContextCatalogCreateResponseDoesNotEchoServerManagedFields(t *testing.T) {
	handler, _, cookie := newAuthenticatedAdminHandler(t, RoleAuditor, "", nil)
	req := httptest.NewRequest(http.MethodPost, "/admin/api/context-catalog", bytes.NewBufferString(`{
		"id":99,
		"context_type":"repo",
		"name":"server-fields",
		"created_at":"client-created",
		"updated_at":"client-updated"
	}`))
	addAuthenticatedCookies(req, cookie)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body = %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "99") || strings.Contains(rec.Body.String(), "client-created") || strings.Contains(rec.Body.String(), "client-updated") {
		t.Fatalf("response echoed client-supplied server-managed fields: %s", rec.Body.String())
	}
}

func TestContextCatalogListReturnsJSONEnvelope(t *testing.T) {
	handler, _, cookie := newAuthenticatedAdminHandler(t, RoleViewer, "", nil)
	req := httptest.NewRequest(http.MethodGet, "/admin/api/context-catalog", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		ContextCatalog []ContextCatalogEntry `json:"context_catalog"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.ContextCatalog == nil {
		t.Fatalf("context_catalog envelope missing in body: %s", rec.Body.String())
	}
}

func TestAuditLogsRequireAdminRole(t *testing.T) {
	handler, _, cookie := newAuthenticatedAdminHandler(t, RoleAuditor, "", nil)
	req := httptest.NewRequest(http.MethodGet, "/admin/api/audit-logs", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func newAuthenticatedReviewHandler(t *testing.T) (Handler, *memoryAdminDB, *http.Cookie) {
	return newAuthenticatedAdminHandler(t, RoleAuditor, "", nil)
}

func newAuthenticatedAdminHandler(t *testing.T, role Role, auditSecret string, evidenceStore evidence.Store) (Handler, *memoryAdminDB, *http.Cookie) {
	t.Helper()
	passwordHash, err := HashPassword("secret-password")
	if err != nil {
		t.Fatalf("HashPassword error: %v", err)
	}
	db := &memoryAdminDB{
		user: User{ID: 1, Username: "alice", PasswordHash: passwordHash, DisplayName: "Alice", Role: role, Status: "active"},
	}
	repo := NewRepository(db)
	auth := Auth{
		Repo:          repo,
		SessionSecret: "session-secret-0123456789abcdef",
		CookieName:    "audit_admin_session",
		Now: func() time.Time {
			return time.Unix(1000, 0).UTC()
		},
	}
	handler := NewHandler(HandlerConfig{Repo: repo, Auth: auth, AuditSecret: auditSecret, EvidenceStore: evidenceStore})

	loginReq := httptest.NewRequest(http.MethodPost, "/admin/api/login", bytes.NewBufferString(`{"username":"alice","password":"secret-password"}`))
	loginRec := httptest.NewRecorder()
	handler.ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", loginRec.Code, loginRec.Body.String())
	}
	var sessionCookie *http.Cookie
	var csrfCookie *http.Cookie
	for _, cookie := range loginRec.Result().Cookies() {
		switch cookie.Name {
		case "audit_admin_session":
			sessionCookie = cookie
		case "audit_admin_csrf":
			csrfCookie = cookie
		}
	}
	if sessionCookie == nil || csrfCookie == nil {
		t.Fatalf("login cookies = %#v", loginRec.Result().Cookies())
	}
	sessionCookie.Unparsed = append(sessionCookie.Unparsed, csrfCookie.Value)
	return handler, db, sessionCookie
}

func addAuthenticatedCookies(req *http.Request, cookie *http.Cookie) {
	req.AddCookie(cookie)
	if len(cookie.Unparsed) == 0 || cookie.Unparsed[0] == "" {
		return
	}
	req.Header.Set("X-CSRF-Token", cookie.Unparsed[0])
	req.AddCookie(&http.Cookie{Name: "audit_admin_csrf", Value: cookie.Unparsed[0]})
}

type fakeEvidenceStore struct {
	body string
	err  error
}

func (s fakeEvidenceStore) Put(ctx context.Context, req evidence.PutRequest) (evidence.Object, error) {
	return evidence.Object{}, errors.New("not implemented")
}

func (s fakeEvidenceStore) Get(ctx context.Context, objectRef string) (io.ReadCloser, error) {
	if s.err != nil {
		return nil, s.err
	}
	return io.NopCloser(strings.NewReader(s.body)), nil
}

type recordingEvidenceStore struct {
	bodies       map[string]string
	requestedRef string
}

func (s *recordingEvidenceStore) Put(ctx context.Context, req evidence.PutRequest) (evidence.Object, error) {
	return evidence.Object{}, errors.New("not implemented")
}

func (s *recordingEvidenceStore) Get(ctx context.Context, objectRef string) (io.ReadCloser, error) {
	s.requestedRef = objectRef
	body, ok := s.bodies[objectRef]
	if !ok {
		return nil, errors.New("object not found")
	}
	return io.NopCloser(strings.NewReader(body)), nil
}

type memoryAdminDB struct {
	user                    User
	session                 Session
	revokedSessionID        string
	auditActions            []string
	auditMetadata           []string
	auditLogs               []AuditActionLog
	reviewDecisions         []ReviewDecision
	contextEntry            ContextCatalogEntry
	rawEvidenceObject       EvidenceObjectSummary
	rawEvidenceErr          error
	rawEvidenceSQL          string
	rawEvidenceArgs         []any
	lookupTokenFingerprint  string
	auditErr                error
	findUserErr             error
	revokeErr               error
	updatedPasswordHash     string
	updatedPasswordUserID   int64
	revokedOtherUserID      int64
	revokedOtherKeepSession string
	revokedOtherAt          time.Time
	updatePasswordErr       error
	revokeOtherErr          error
	passwordChangeOps       []string
	traceDetail             TraceDetail
	usageModelFilter        string
	employeeUsageFilter     EmployeeUsageFilter
	employeeUsageCalled     bool
}

func (m *memoryAdminDB) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	switch {
	case strings.Contains(sql, "INSERT INTO audit_sessions"):
		m.session = Session{
			SessionID: args[0].(string),
			UserID:    args[1].(int64),
			ExpiresAt: args[2].(time.Time),
		}
		if len(args) > 3 {
			m.session.CSRFToken = args[3].(string)
		}
	case strings.Contains(sql, "WITH updated_user AS") && strings.Contains(sql, "revoked_sessions AS"):
		if m.revokeOtherErr != nil {
			return pgconn.CommandTag{}, m.revokeOtherErr
		}
		if m.updatePasswordErr != nil {
			return pgconn.CommandTag{}, m.updatePasswordErr
		}
		if m.auditErr != nil {
			return pgconn.CommandTag{}, m.auditErr
		}
		m.passwordChangeOps = append(m.passwordChangeOps, "revoke_other_sessions", "update_password", "audit_log")
		m.revokedOtherUserID = args[0].(int64)
		m.revokedOtherKeepSession = args[3].(string)
		m.revokedOtherAt = args[2].(time.Time)
		m.updatedPasswordUserID = args[0].(int64)
		m.updatedPasswordHash = args[1].(string)
		m.user.PasswordHash = args[1].(string)
		m.auditActions = append(m.auditActions, args[6].(string))
		m.auditMetadata = append(m.auditMetadata, args[14].(string))
		m.auditLogs = append(m.auditLogs, AuditActionLog{
			ActorUserID:        args[4].(int64),
			ActorUsername:      args[5].(string),
			Action:             args[6].(string),
			TargetType:         args[7].(string),
			TargetID:           args[8].(string),
			TokenFingerprint:   args[9].(string),
			FingerprintDisplay: args[10].(string),
			TraceID:            args[11].(string),
			IPHash:             args[12].(string),
			UserAgentHash:      args[13].(string),
			MetadataJSON:       args[14].(string),
			CreatedAt:          args[15].(time.Time),
		})
	case strings.Contains(sql, "UPDATE audit_users") && strings.Contains(sql, "password_hash"):
		m.passwordChangeOps = append(m.passwordChangeOps, "update_password")
		if m.updatePasswordErr != nil {
			return pgconn.CommandTag{}, m.updatePasswordErr
		}
		m.updatedPasswordUserID = args[0].(int64)
		m.updatedPasswordHash = args[1].(string)
		m.user.PasswordHash = args[1].(string)
	case strings.Contains(sql, "UPDATE audit_sessions") && strings.Contains(sql, "session_id <>"):
		m.passwordChangeOps = append(m.passwordChangeOps, "revoke_other_sessions")
		if m.revokeOtherErr != nil {
			return pgconn.CommandTag{}, m.revokeOtherErr
		}
		m.revokedOtherUserID = args[0].(int64)
		m.revokedOtherKeepSession = args[1].(string)
		m.revokedOtherAt = args[2].(time.Time)
	case strings.Contains(sql, "UPDATE audit_sessions SET revoked_at"):
		if m.revokeErr != nil {
			return pgconn.CommandTag{}, m.revokeErr
		}
		m.revokedSessionID = args[0].(string)
	case strings.Contains(sql, "INSERT INTO audit_action_logs"):
		action := args[2].(string)
		if action == "password_changed" {
			m.passwordChangeOps = append(m.passwordChangeOps, "audit_log")
		}
		if m.auditErr != nil {
			return pgconn.CommandTag{}, m.auditErr
		}
		m.auditActions = append(m.auditActions, action)
		m.auditMetadata = append(m.auditMetadata, args[10].(string))
		m.auditLogs = append(m.auditLogs, AuditActionLog{
			ActorUserID:        args[0].(int64),
			ActorUsername:      args[1].(string),
			Action:             args[2].(string),
			TargetType:         args[3].(string),
			TargetID:           args[4].(string),
			TokenFingerprint:   args[5].(string),
			FingerprintDisplay: args[6].(string),
			TraceID:            args[7].(string),
			IPHash:             args[8].(string),
			UserAgentHash:      args[9].(string),
			MetadataJSON:       args[10].(string),
			CreatedAt:          args[11].(time.Time),
		})
	case strings.Contains(sql, "INSERT INTO review_decisions"):
		m.reviewDecisions = append(m.reviewDecisions, ReviewDecision{
			TargetType:       args[0].(string),
			TargetID:         args[1].(string),
			Decision:         args[2].(string),
			ReviewerID:       args[3].(int64),
			ReviewerUsername: args[4].(string),
			Note:             args[5].(string),
			CreatedAt:        args[6].(time.Time),
		})
	case strings.Contains(sql, "INSERT INTO context_catalog"):
		m.contextEntry = ContextCatalogEntry{
			ContextType:            args[0].(string),
			Name:                   args[1].(string),
			Description:            args[2].(string),
			Keywords:               args[3].([]string),
			Aliases:                args[4].([]string),
			Owner:                  args[5].(string),
			ExpectedTaskCategories: args[6].([]string),
			ExpectedModels:         args[7].([]string),
			ExpectedUsageLevel:     args[8].(string),
			Active:                 args[9].(bool),
			CreatedBy:              args[10].(string),
			UpdatedBy:              args[11].(string),
		}
	}
	return pgconn.NewCommandTag("INSERT 0 1"), nil
}

func (m *memoryAdminDB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if strings.Contains(sql, "FROM usage_aggregates") &&
		!strings.Contains(sql, "SELECT DISTINCT model") &&
		!strings.Contains(sql, "GROUP BY bucket_start") &&
		!strings.Contains(sql, "GROUP BY model") {
		for i, arg := range args {
			if strings.Contains(sql, fmt.Sprintf("model = $%d", i+1)) {
				m.usageModelFilter = arg.(string)
				break
			}
		}
	}
	if strings.Contains(sql, "SELECT DISTINCT model") && strings.Contains(sql, "FROM usage_aggregates") {
		m.employeeUsageCalled = true
		m.employeeUsageFilter = EmployeeUsageFilter{Username: args[0].(string), Start: args[1].(time.Time), End: args[2].(time.Time)}
		return &scanRows{scans: []func(dest ...any) error{
			func(dest ...any) error {
				*(dest[0].(*string)) = "gpt-5.2"
				return nil
			},
			func(dest ...any) error {
				*(dest[0].(*string)) = "claude-4-sonnet"
				return nil
			},
		}}, nil
	}
	if strings.Contains(sql, "GROUP BY bucket_start") && strings.Contains(sql, "FROM usage_aggregates") {
		if len(args) > 3 {
			m.employeeUsageFilter.Model = args[3].(string)
		}
		return &scanRows{scans: []func(dest ...any) error{
			func(dest ...any) error {
				*(dest[0].(*string)) = "2026-05-29 00:00:00+00"
				*(dest[1].(*int64)) = int64(2)
				*(dest[2].(*int64)) = int64(2)
				*(dest[3].(*int64)) = int64(0)
				*(dest[4].(*int64)) = int64(10)
				*(dest[5].(*int64)) = int64(5)
				*(dest[6].(*int64)) = int64(3)
				*(dest[7].(*int64)) = int64(15)
				return nil
			},
		}}, nil
	}
	if strings.Contains(sql, "GROUP BY model") && strings.Contains(sql, "FROM usage_aggregates") {
		return &scanRows{scans: []func(dest ...any) error{
			func(dest ...any) error {
				*(dest[0].(*string)) = "gpt-5.2"
				*(dest[1].(*int64)) = int64(2)
				*(dest[2].(*int64)) = int64(2)
				*(dest[3].(*int64)) = int64(0)
				*(dest[4].(*int64)) = int64(10)
				*(dest[5].(*int64)) = int64(5)
				*(dest[6].(*int64)) = int64(3)
				*(dest[7].(*int64)) = int64(15)
				return nil
			},
		}}, nil
	}
	return &fakeRows{}, nil
}

func (m *memoryAdminDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return memoryAdminRow{db: m, sql: sql, args: args}
}

type memoryAdminRow struct {
	db   *memoryAdminDB
	sql  string
	args []any
}

func (r memoryAdminRow) Scan(dest ...any) error {
	if strings.Contains(r.sql, "FROM audit_users") {
		if r.db.findUserErr != nil {
			return r.db.findUserErr
		}
		if strings.Contains(r.sql, "WHERE username = $1") {
			username := r.args[0].(string)
			if username != r.db.user.Username || r.db.user.Status != "active" {
				return pgx.ErrNoRows
			}
		} else {
			userID := r.args[0].(int64)
			if userID != r.db.user.ID || r.db.user.Status != "active" {
				return pgx.ErrNoRows
			}
		}
		*(dest[0].(*int64)) = r.db.user.ID
		*(dest[1].(*string)) = r.db.user.Username
		*(dest[2].(*string)) = r.db.user.PasswordHash
		*(dest[3].(*string)) = r.db.user.DisplayName
		*(dest[4].(*string)) = r.db.user.Email
		*(dest[5].(*Role)) = r.db.user.Role
		*(dest[6].(*string)) = r.db.user.Status
		*(dest[7].(*time.Time)) = time.Unix(900, 0).UTC()
		*(dest[8].(*time.Time)) = time.Unix(900, 0).UTC()
		return nil
	}
	if strings.Contains(r.sql, "FROM audit_sessions") {
		sessionID := r.args[0].(string)
		now := r.args[1].(time.Time)
		if sessionID != r.db.session.SessionID || sessionID == r.db.revokedSessionID || !r.db.session.ExpiresAt.After(now) {
			return pgx.ErrNoRows
		}
		*(dest[0].(*int64)) = r.db.user.ID
		*(dest[1].(*string)) = r.db.user.Username
		*(dest[2].(*string)) = r.db.user.DisplayName
		*(dest[3].(*Role)) = r.db.user.Role
		if len(dest) > 4 {
			*(dest[4].(*string)) = r.db.session.CSRFToken
		}
		return nil
	}
	if strings.Contains(r.sql, "FROM token_identity_cache") {
		r.db.lookupTokenFingerprint = r.args[0].(string)
		if len(dest) >= 4 {
			*(dest[0].(*string)) = "E10001"
			*(dest[1].(*int)) = 42
			*(dest[2].(*string)) = "prod key"
			*(dest[3].(*int)) = 1
		}
		return nil
	}
	if strings.Contains(r.sql, "FROM raw_evidence_objects") {
		r.db.rawEvidenceSQL = r.sql
		r.db.rawEvidenceArgs = append([]any(nil), r.args...)
		if r.db.rawEvidenceErr != nil {
			return r.db.rawEvidenceErr
		}
		if r.db.rawEvidenceObject.ObjectRef == "" {
			return pgx.ErrNoRows
		}
		*(dest[0].(*string)) = r.db.rawEvidenceObject.TraceID
		*(dest[1].(*string)) = r.db.rawEvidenceObject.ObjectType
		*(dest[2].(*string)) = r.db.rawEvidenceObject.ObjectRef
		*(dest[3].(*string)) = r.db.rawEvidenceObject.ContentType
		*(dest[4].(*int64)) = r.db.rawEvidenceObject.SizeBytes
		*(dest[5].(*string)) = r.db.rawEvidenceObject.SHA256
		return nil
	}
	if strings.Contains(r.sql, "request_raw_ref") && strings.Contains(r.sql, "FROM traces") {
		if r.db.traceDetail.TraceID == "" {
			return pgx.ErrNoRows
		}
		detail := r.db.traceDetail
		*(dest[0].(*string)) = detail.TraceID
		*(dest[1].(*string)) = detail.Method
		*(dest[2].(*string)) = detail.Path
		*(dest[3].(*string)) = detail.RoutePattern
		*(dest[4].(*string)) = detail.ProtocolFamily
		*(dest[5].(*int)) = detail.StatusCode
		*(dest[6].(*string)) = detail.Username
		*(dest[7].(*string)) = detail.FingerprintDisplay
		*(dest[8].(*string)) = detail.ModelRequested
		*(dest[9].(*int)) = detail.UsagePromptTokens
		*(dest[10].(*int)) = detail.UsageCompletionTokens
		*(dest[11].(*int)) = detail.UsageCachedTokens
		*(dest[12].(*int)) = detail.UsageTotalTokens
		*(dest[13].(*string)) = detail.CreatedAt
		*(dest[14].(*string)) = detail.RequestRawRef
		*(dest[15].(*string)) = detail.ResponseRawRef
		*(dest[16].(*string)) = detail.RequestHeadersRef
		*(dest[17].(*string)) = detail.ResponseHeadersRef
		*(dest[18].(*string)) = detail.IdentityResolutionStatus
		*(dest[19].(*string)) = detail.AnalysisStatus
		return nil
	}
	if strings.Contains(r.sql, "FROM traces") {
		*(dest[0].(*int64)) = 0
		*(dest[1].(*int64)) = 0
		*(dest[2].(*int64)) = 0
		*(dest[3].(*int64)) = 0
		*(dest[4].(*int64)) = 0
		*(dest[5].(*int64)) = 0
		*(dest[6].(*int64)) = 0
		return nil
	}
	if strings.Contains(r.sql, "FROM usage_anomalies") {
		r.db.lookupTokenFingerprint = r.args[0].(string)
		if len(dest) >= 1 {
			*(dest[0].(*int)) = 0
		}
		return nil
	}
	return pgx.ErrNoRows
}

func traceDetailWithRawRefs() TraceDetail {
	return TraceDetail{
		TraceSummary: TraceSummary{
			TraceID:               "trace_123",
			Method:                http.MethodPost,
			Path:                  "/v1/chat/completions",
			RoutePattern:          "/v1/chat/completions",
			ProtocolFamily:        "openai",
			StatusCode:            http.StatusOK,
			Username:              "E10001",
			FingerprintDisplay:    "fp_1234",
			ModelRequested:        "gpt-5",
			UsagePromptTokens:     12,
			UsageCompletionTokens: 23,
			UsageCachedTokens:     7,
			UsageTotalTokens:      42,
			CreatedAt:             "2026-04-28 10:00:00+00",
		},
		RequestRawRef:            "raw/trace_123/request_body.bin",
		ResponseRawRef:           "raw/trace_123/response_body.bin",
		RequestHeadersRef:        "raw/trace_123/request_headers.json",
		ResponseHeadersRef:       "raw/trace_123/response_headers.json",
		IdentityResolutionStatus: "resolved",
		AnalysisStatus:           "complete",
	}
}

func rawRefValues() []string {
	return []string{
		"raw/trace_123/request_body.bin",
		"raw/trace_123/response_body.bin",
		"raw/trace_123/request_headers.json",
		"raw/trace_123/response_headers.json",
	}
}

func assertClearSessionCookie(t *testing.T, cookies []*http.Cookie) {
	t.Helper()
	for _, cookie := range cookies {
		if cookie.Name != "audit_admin_session" {
			continue
		}
		if cookie.Value != "" || cookie.MaxAge >= 0 {
			t.Fatalf("clear cookie = %#v", cookie)
		}
		return
	}
	t.Fatalf("clear cookie not found in %#v", cookies)
}
