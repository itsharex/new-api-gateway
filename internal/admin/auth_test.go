package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSignAndVerifySessionCookie(t *testing.T) {
	auth := Auth{SessionSecret: "session-secret-0123456789abcdef", CookieName: "audit_admin_session"}
	cookie := auth.sessionCookie("sess_123", time.Unix(2000, 0).UTC())

	sessionID, ok := auth.verifyCookie(cookie.Value)
	if !ok {
		t.Fatal("verifyCookie rejected signed cookie")
	}
	if sessionID != "sess_123" {
		t.Fatalf("sessionID = %q", sessionID)
	}
	if _, ok := auth.verifyCookie(cookie.Value + "tampered"); ok {
		t.Fatal("verifyCookie accepted tampered cookie")
	}
}

func TestRequirePermissionRejectsMissingPermission(t *testing.T) {
	auth := Auth{SessionSecret: "session-secret-0123456789abcdef", CookieName: "audit_admin_session"}
	handlerCalled := false
	handler := auth.Require(PermissionRawEvidence, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
	}))
	req := httptest.NewRequest(http.MethodGet, "/admin/api/raw", nil)
	req = req.WithContext(WithPrincipal(req.Context(), Principal{UserID: 1, Username: "viewer", Role: RoleViewer}))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if handlerCalled {
		t.Fatal("handler was called despite missing permission")
	}
}

func TestMiddlewareStoresSessionIDInPrincipal(t *testing.T) {
	passwordHash, err := HashPassword("secret-password")
	if err != nil {
		t.Fatalf("HashPassword error: %v", err)
	}
	db := &memoryAdminDB{
		user: User{ID: 1, Username: "alice", PasswordHash: passwordHash, DisplayName: "Alice", Role: RoleAuditor, Status: "active"},
		session: Session{
			SessionID: "sess_123",
			UserID:    1,
			ExpiresAt: time.Unix(2000, 0).UTC(),
			CSRFToken: "csrf_123",
		},
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
	cookie := auth.sessionCookie("sess_123", time.Unix(2000, 0).UTC())
	handler := auth.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := PrincipalFromContext(r.Context())
		if !ok {
			t.Fatal("principal missing from request context")
		}
		if principal.SessionID != "sess_123" {
			t.Fatalf("SessionID = %q, want sess_123", principal.SessionID)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodGet, "/admin/api/me", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
}
