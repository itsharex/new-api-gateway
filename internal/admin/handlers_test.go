package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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
	if len(cookies) != 1 || cookies[0].Name != "audit_admin_session" {
		t.Fatalf("cookies = %#v", cookies)
	}

	meReq := httptest.NewRequest(http.MethodGet, "/admin/api/me", nil)
	meReq.AddCookie(cookies[0])
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
	logoutReq.AddCookie(cookies[0])
	logoutRec := httptest.NewRecorder()
	handler.ServeHTTP(logoutRec, logoutReq)
	if logoutRec.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d", logoutRec.Code)
	}
	if db.revokedSessionID == "" {
		t.Fatal("session was not revoked")
	}
}

type memoryAdminDB struct {
	user             User
	session          Session
	revokedSessionID string
	auditActions     []string
}

func (m *memoryAdminDB) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	switch {
	case strings.Contains(sql, "INSERT INTO audit_sessions"):
		m.session = Session{
			SessionID: args[0].(string),
			UserID:    args[1].(int64),
			ExpiresAt: args[2].(time.Time),
		}
	case strings.Contains(sql, "UPDATE audit_sessions SET revoked_at"):
		m.revokedSessionID = args[0].(string)
	case strings.Contains(sql, "INSERT INTO audit_action_logs"):
		m.auditActions = append(m.auditActions, args[2].(string))
	}
	return pgconn.NewCommandTag("INSERT 0 1"), nil
}

func (m *memoryAdminDB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
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
		username := r.args[0].(string)
		if username != r.db.user.Username || r.db.user.Status != "active" {
			return pgx.ErrNoRows
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
		return nil
	}
	return pgx.ErrNoRows
}
