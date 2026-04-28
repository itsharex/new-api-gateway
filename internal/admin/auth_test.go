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
