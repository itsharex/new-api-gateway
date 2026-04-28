package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
			req.AddCookie(cookie)
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
	req.AddCookie(cookie)
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
	wantFingerprint := fingerprint.Compute("secret-plain-text", auditSecret)

	req := httptest.NewRequest(http.MethodPost, "/admin/api/api-key-lookup", bytes.NewBufferString(`{"api_key":"`+plaintextKey+`"}`))
	req.AddCookie(cookie)
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
	return handler, db, loginRec.Result().Cookies()[0]
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

type memoryAdminDB struct {
	user                   User
	session                Session
	revokedSessionID       string
	auditActions           []string
	auditMetadata          []string
	auditLogs              []AuditActionLog
	reviewDecisions        []ReviewDecision
	rawEvidenceObject      EvidenceObjectSummary
	rawEvidenceErr         error
	lookupTokenFingerprint string
	findUserErr            error
	revokeErr              error
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
		if m.revokeErr != nil {
			return pgconn.CommandTag{}, m.revokeErr
		}
		m.revokedSessionID = args[0].(string)
	case strings.Contains(sql, "INSERT INTO audit_action_logs"):
		m.auditActions = append(m.auditActions, args[2].(string))
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
		if r.db.findUserErr != nil {
			return r.db.findUserErr
		}
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
	if strings.Contains(r.sql, "FROM usage_anomalies") {
		r.db.lookupTokenFingerprint = r.args[0].(string)
		if len(dest) >= 1 {
			*(dest[0].(*int)) = 0
		}
		return nil
	}
	if strings.Contains(r.sql, "FROM raw_evidence_objects") {
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
	return pgx.ErrNoRows
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
