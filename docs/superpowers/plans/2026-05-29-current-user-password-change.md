# Current User Password Change Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a self-service admin UI feature that lets the current logged-in user change their local password after entering their current password.

**Architecture:** Add one authenticated CSRF-protected endpoint under `/admin/api/me/password`. The endpoint reloads the current user, verifies the current password, validates and hashes the new password, updates `audit_users`, revokes the user's other sessions while preserving the current one, and writes an audit log. The UI adds a small self-service password form reachable from the sidebar user panel.

**Tech Stack:** Go `net/http`, pgx repository pattern, bcrypt helpers in `internal/admin/passwords.go`, vanilla JS admin UI in `internal/adminui/app.js`, existing CSS in `internal/adminui/app.css`, Go unit tests with `go test`.

---

## File Structure

- Modify `internal/admin/models.go`: add server-only `SessionID` to `Principal`.
- Modify `internal/admin/auth.go`: populate `Principal.SessionID` in `Auth.Middleware`.
- Modify `internal/admin/repository.go`: add user lookup, password update, and other-session revocation methods.
- Modify `internal/admin/handlers.go`: register and implement `POST /admin/api/me/password`.
- Modify `internal/admin/auth_test.go`: prove middleware stores the verified session id in context.
- Modify `internal/admin/repository_test.go`: cover new repository SQL and nil DB behavior.
- Modify `internal/admin/handlers_test.go`: cover success, validation failures, auth failures, CSRF, audit log, and session preservation.
- Modify `internal/adminui/app.js`: add password-change view, sidebar entry, form handling, success and error states.
- Modify `internal/adminui/app.css`: add minimal layout styling for the user-panel actions and success message if needed.
- Modify `ARCHITECTURE.md`: document the new admin API endpoint.
- Inspect `README.md` and `CLAUDE.md`: update only if they contain admin endpoint or password rotation notes.

---

### Task 1: Propagate Current Session ID To Authenticated Handlers

**Files:**
- Modify: `internal/admin/models.go`
- Modify: `internal/admin/auth.go`
- Test: `internal/admin/auth_test.go`

- [ ] **Step 1: Write the failing middleware test**

Append this test to `internal/admin/auth_test.go`:

```go
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
```

- [ ] **Step 2: Run the focused test and verify it fails**

Run:

```bash
go test ./internal/admin -run TestMiddlewareStoresSessionIDInPrincipal -count=1
```

Expected: FAIL with `SessionID = "", want sess_123`.

- [ ] **Step 3: Add server-only session id to `Principal`**

Change `Principal` in `internal/admin/models.go` to:

```go
type Principal struct {
	UserID      int64  `json:"user_id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Role        Role   `json:"role"`
	CSRFToken   string `json:"-"`
	SessionID   string `json:"-"`
}
```

- [ ] **Step 4: Populate the session id in middleware**

In `internal/admin/auth.go`, change the successful middleware path to:

```go
principal, err := a.Repo.PrincipalBySession(r.Context(), sessionID, a.now())
if err != nil {
	http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
	return
}
principal.SessionID = sessionID
next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), principal)))
```

- [ ] **Step 5: Run the focused test and verify it passes**

Run:

```bash
go test ./internal/admin -run TestMiddlewareStoresSessionIDInPrincipal -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit Task 1**

Run:

```bash
git add internal/admin/models.go internal/admin/auth.go internal/admin/auth_test.go
git commit -m "feat(admin): expose current session to handlers"
```

---

### Task 2: Add Repository Methods For Self-Service Password Change

**Files:**
- Modify: `internal/admin/repository.go`
- Test: `internal/admin/repository_test.go`

- [ ] **Step 1: Write repository tests for active-user lookup by id**

Add these tests near the existing repository user/session tests in `internal/admin/repository_test.go`:

```go
func TestRepositoryFindActiveUserByID(t *testing.T) {
	db := &recordingAdminDB{
		row: scanFuncRow{scan: func(dest ...any) error {
			*(dest[0].(*int64)) = int64(7)
			*(dest[1].(*string)) = "alice"
			*(dest[2].(*string)) = "$2a$10$abcdefghijklmnopqrstuu"
			*(dest[3].(*string)) = "Alice"
			*(dest[4].(*string)) = "alice@example.com"
			*(dest[5].(*Role)) = RoleAuditor
			*(dest[6].(*string)) = "active"
			*(dest[7].(*time.Time)) = time.Unix(100, 0).UTC()
			*(dest[8].(*time.Time)) = time.Unix(200, 0).UTC()
			return nil
		}},
	}
	repo := NewRepository(db)

	user, err := repo.FindActiveUserByID(context.Background(), 7)

	if err != nil {
		t.Fatalf("FindActiveUserByID error: %v", err)
	}
	if user.ID != 7 || user.Username != "alice" || user.Role != RoleAuditor {
		t.Fatalf("user = %#v", user)
	}
	if !strings.Contains(db.querySQL, "FROM audit_users") {
		t.Fatalf("querySQL = %s", db.querySQL)
	}
	if !strings.Contains(db.querySQL, "WHERE id = $1 AND status = 'active'") {
		t.Fatalf("querySQL missing active id predicate: %s", db.querySQL)
	}
	if len(db.queryArgs) != 1 || db.queryArgs[0] != int64(7) {
		t.Fatalf("queryArgs = %#v", db.queryArgs)
	}
}

func TestRepositoryFindActiveUserByIDRequiresDB(t *testing.T) {
	repo := NewRepository(nil)

	_, err := repo.FindActiveUserByID(context.Background(), 7)

	if !errors.Is(err, ErrAdminDBRequired) {
		t.Fatalf("FindActiveUserByID error = %v, want ErrAdminDBRequired", err)
	}
}
```

- [ ] **Step 2: Write repository tests for password update and other-session revocation**

Add these tests to `internal/admin/repository_test.go`:

```go
func TestRepositoryUpdateUserPassword(t *testing.T) {
	db := &recordingAdminDB{}
	repo := NewRepository(db)
	now := time.Unix(3000, 0).UTC()

	err := repo.UpdateUserPassword(context.Background(), 7, "$2a$10$newhashabcdefghijkl", now)

	if err != nil {
		t.Fatalf("UpdateUserPassword error: %v", err)
	}
	if !strings.Contains(db.sql, "UPDATE audit_users") {
		t.Fatalf("sql = %s", db.sql)
	}
	if !strings.Contains(db.sql, "SET password_hash = $2, updated_at = $3") {
		t.Fatalf("sql missing password update assignment: %s", db.sql)
	}
	if !strings.Contains(db.sql, "WHERE id = $1") {
		t.Fatalf("sql missing user id predicate: %s", db.sql)
	}
	if len(db.args) != 3 || db.args[0] != int64(7) || db.args[1] != "$2a$10$newhashabcdefghijkl" || db.args[2] != now {
		t.Fatalf("args = %#v", db.args)
	}
}

func TestRepositoryUpdateUserPasswordRequiresDB(t *testing.T) {
	repo := NewRepository(nil)

	err := repo.UpdateUserPassword(context.Background(), 7, "$2a$10$newhashabcdefghijkl", time.Unix(3000, 0).UTC())

	if !errors.Is(err, ErrAdminDBRequired) {
		t.Fatalf("UpdateUserPassword error = %v, want ErrAdminDBRequired", err)
	}
}

func TestRepositoryRevokeOtherSessions(t *testing.T) {
	db := &recordingAdminDB{}
	repo := NewRepository(db)
	now := time.Unix(3000, 0).UTC()

	err := repo.RevokeOtherSessions(context.Background(), 7, "sess_keep", now)

	if err != nil {
		t.Fatalf("RevokeOtherSessions error: %v", err)
	}
	if !strings.Contains(db.sql, "UPDATE audit_sessions") {
		t.Fatalf("sql = %s", db.sql)
	}
	for _, fragment := range []string{
		"user_id = $1",
		"session_id <> $2",
		"revoked_at IS NULL",
		"expires_at > $3",
	} {
		if !strings.Contains(db.sql, fragment) {
			t.Fatalf("sql missing %q: %s", fragment, db.sql)
		}
	}
	if len(db.args) != 3 || db.args[0] != int64(7) || db.args[1] != "sess_keep" || db.args[2] != now {
		t.Fatalf("args = %#v", db.args)
	}
}

func TestRepositoryRevokeOtherSessionsRequiresDB(t *testing.T) {
	repo := NewRepository(nil)

	err := repo.RevokeOtherSessions(context.Background(), 7, "sess_keep", time.Unix(3000, 0).UTC())

	if !errors.Is(err, ErrAdminDBRequired) {
		t.Fatalf("RevokeOtherSessions error = %v, want ErrAdminDBRequired", err)
	}
}
```

- [ ] **Step 3: Run repository tests and verify they fail**

Run:

```bash
go test ./internal/admin -run 'TestRepository(FindActiveUserByID|UpdateUserPassword|RevokeOtherSessions)' -count=1
```

Expected: FAIL with undefined methods on `Repository`.

- [ ] **Step 4: Implement repository methods**

Add these methods to `internal/admin/repository.go` after `FindActiveUserByUsername` and after `RevokeSession`:

```go
func (r Repository) FindActiveUserByID(ctx context.Context, userID int64) (User, error) {
	if r.db == nil {
		return User{}, ErrAdminDBRequired
	}
	var user User
	err := r.db.QueryRow(ctx, `
SELECT id, username, password_hash, display_name, email, role, status, created_at, updated_at
FROM audit_users
WHERE id = $1 AND status = 'active'
LIMIT 1`, userID).Scan(
		&user.ID, &user.Username, &user.PasswordHash, &user.DisplayName, &user.Email,
		&user.Role, &user.Status, &user.CreatedAt, &user.UpdatedAt,
	)
	return user, err
}
```

```go
func (r Repository) UpdateUserPassword(ctx context.Context, userID int64, passwordHash string, now time.Time) error {
	if r.db == nil {
		return ErrAdminDBRequired
	}
	_, err := r.db.Exec(ctx, `
UPDATE audit_users
SET password_hash = $2, updated_at = $3
WHERE id = $1`, userID, passwordHash, now)
	return err
}

func (r Repository) RevokeOtherSessions(ctx context.Context, userID int64, keepSessionID string, now time.Time) error {
	if r.db == nil {
		return ErrAdminDBRequired
	}
	_, err := r.db.Exec(ctx, `
UPDATE audit_sessions
SET revoked_at = $3
WHERE user_id = $1
  AND session_id <> $2
  AND revoked_at IS NULL
  AND expires_at > $3`, userID, keepSessionID, now)
	return err
}
```

- [ ] **Step 5: Run repository tests and verify they pass**

Run:

```bash
go test ./internal/admin -run 'TestRepository(FindActiveUserByID|UpdateUserPassword|RevokeOtherSessions)' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit Task 2**

Run:

```bash
git add internal/admin/repository.go internal/admin/repository_test.go
git commit -m "feat(admin): add password change repository methods"
```

---

### Task 3: Add Password Change Handler And Route

**Files:**
- Modify: `internal/admin/handlers.go`
- Modify: `internal/admin/handlers_test.go`

- [ ] **Step 1: Extend `memoryAdminDB` test helper**

In `internal/admin/handlers_test.go`, add these fields to `memoryAdminDB`:

```go
	updatedPasswordHash     string
	updatedPasswordUserID   int64
	revokedOtherUserID      int64
	revokedOtherKeepSession string
	revokedOtherAt          time.Time
	updatePasswordErr       error
	revokeOtherErr          error
```

In `memoryAdminDB.Exec`, place these cases before the existing generic `UPDATE audit_sessions SET revoked_at` case:

```go
	case strings.Contains(sql, "UPDATE audit_users") && strings.Contains(sql, "password_hash"):
		if m.updatePasswordErr != nil {
			return pgconn.CommandTag{}, m.updatePasswordErr
		}
		m.updatedPasswordUserID = args[0].(int64)
		m.updatedPasswordHash = args[1].(string)
		m.user.PasswordHash = args[1].(string)
	case strings.Contains(sql, "UPDATE audit_sessions") && strings.Contains(sql, "session_id <>"):
		if m.revokeOtherErr != nil {
			return pgconn.CommandTag{}, m.revokeOtherErr
		}
		m.revokedOtherUserID = args[0].(int64)
		m.revokedOtherKeepSession = args[1].(string)
		m.revokedOtherAt = args[2].(time.Time)
```

Update `memoryAdminRow.Scan` user lookup handling so it supports both username and id lookups:

```go
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
```

- [ ] **Step 2: Write the success handler test**

Add this test to `internal/admin/handlers_test.go` near login/logout tests:

```go
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

	meReq := httptest.NewRequest(http.MethodGet, "/admin/api/me", nil)
	meReq.AddCookie(cookie)
	meRec := httptest.NewRecorder()
	handler.ServeHTTP(meRec, meReq)
	if meRec.Code != http.StatusOK {
		t.Fatalf("me status after password change = %d, body = %s", meRec.Code, meRec.Body.String())
	}
}
```

- [ ] **Step 3: Write validation and CSRF tests**

Add this table test to `internal/admin/handlers_test.go`:

```go
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
```

- [ ] **Step 4: Run handler tests and verify they fail**

Run:

```bash
go test ./internal/admin -run 'TestChangeCurrentUserPassword' -count=1
```

Expected: FAIL with 404 responses for `/admin/api/me/password` or missing handler implementation.

- [ ] **Step 5: Register the route**

In `internal/admin/handlers.go`, add this route after `GET /admin/api/me`:

```go
mux.Handle("POST /admin/api/me/password", h.auth.Middleware(h.requireCSRF(http.HandlerFunc(h.changePassword))))
```

- [ ] **Step 6: Implement the handler**

Add this method near `me` and `logout` in `internal/admin/handlers.go`:

```go
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
	input.CurrentPassword = strings.TrimSpace(input.CurrentPassword)
	input.NewPassword = strings.TrimSpace(input.NewPassword)
	input.ConfirmPassword = strings.TrimSpace(input.ConfirmPassword)
	if input.CurrentPassword == "" || input.NewPassword == "" || input.ConfirmPassword == "" {
		http.Error(w, "password fields are required", http.StatusBadRequest)
		return
	}
	if len(input.NewPassword) < 12 {
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
	if err := h.repo.UpdateUserPassword(r.Context(), user.ID, newHash, now); err != nil {
		http.Error(w, "failed to change password", http.StatusInternalServerError)
		return
	}
	if err := h.repo.RevokeOtherSessions(r.Context(), user.ID, principal.SessionID, now); err != nil {
		http.Error(w, "failed to change password", http.StatusInternalServerError)
		return
	}
	_ = h.repo.InsertAuditActionLog(r.Context(), AuditActionLog{
		ActorUserID:   user.ID,
		ActorUsername: user.Username,
		Action:        "password_changed",
		TargetType:    "audit_user",
		TargetID:      user.Username,
		MetadataJSON:  `{"revoked_other_sessions":true}`,
		CreatedAt:     now,
	})
	w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 7: Run handler tests and verify they pass**

Run:

```bash
go test ./internal/admin -run 'TestChangeCurrentUserPassword' -count=1
```

Expected: PASS.

- [ ] **Step 8: Run all admin tests**

Run:

```bash
go test ./internal/admin -count=1
```

Expected: PASS.

- [ ] **Step 9: Commit Task 3**

Run:

```bash
git add internal/admin/handlers.go internal/admin/handlers_test.go
git commit -m "feat(admin): add current user password endpoint"
```

---

### Task 4: Add Admin UI Password Change View

**Files:**
- Modify: `internal/adminui/app.js`
- Modify: `internal/adminui/app.css`

- [ ] **Step 1: Add password view state**

In `internal/adminui/app.js`, extend `state` with:

```js
  password: {
    error: "",
    success: "",
  },
```

The top-level state should become:

```js
const state = {
  user: null,
  view: "overview",
  error: "",
  usage: {
    username: "",
    range: "30d",
    model: "",
  },
  password: {
    error: "",
    success: "",
  },
};
```

- [ ] **Step 2: Add sidebar entry**

In `renderShell`, replace the user panel button area with:

```js
          <div class="user-actions">
            <button type="button" id="change-password-button">修改密码</button>
            <button type="button" id="logout-button">退出登录</button>
          </div>
```

Immediately before the logout listener, add:

```js
  document.querySelector("#change-password-button").addEventListener("click", () => {
    state.view = "password";
    state.error = "";
    state.password.error = "";
    state.password.success = "";
    renderPasswordChange();
  });
```

- [ ] **Step 3: Route the password view through `loadView`**

In `loadView`, add this branch before the final audit branch:

```js
    } else if (state.view === "password") {
      renderPasswordChange();
```

Update `currentView` so the loading label does not fall back to "概览" for the password view:

```js
function currentView() {
  if (state.view === "password") return { id: "password", label: "修改密码" };
  return views.find((view) => view.id === state.view) || views[0];
}
```

- [ ] **Step 4: Add the password form renderer**

Add this function near `renderSettings` in `internal/adminui/app.js`:

```js
function renderPasswordChange() {
  const message = state.password.success
    ? `<div class="success">${escapeHTML(state.password.success)}</div>`
    : "";
  const error = state.password.error
    ? `<div class="error">${escapeHTML(state.password.error)}</div>`
    : "";
  renderShell(page("修改密码", `
    <section class="panel password-panel">
      <form id="password-form" class="stacked-form">
        <div class="field">
          <label for="current_password">当前密码</label>
          <input id="current_password" name="current_password" type="password" autocomplete="current-password" required>
        </div>
        <div class="field">
          <label for="new_password">新密码</label>
          <input id="new_password" name="new_password" type="password" autocomplete="new-password" minlength="12" required>
        </div>
        <div class="field">
          <label for="confirm_password">确认新密码</label>
          <input id="confirm_password" name="confirm_password" type="password" autocomplete="new-password" minlength="12" required>
        </div>
        ${error}
        ${message}
        <div class="field">
          <button class="primary" type="submit">保存新密码</button>
        </div>
      </form>
    </section>
  `));

  document.querySelector("#password-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    state.password.error = "";
    state.password.success = "";
    const form = new FormData(event.currentTarget);
    const currentPassword = String(form.get("current_password") || "");
    const newPassword = String(form.get("new_password") || "");
    const confirmPassword = String(form.get("confirm_password") || "");
    if (!currentPassword || !newPassword || !confirmPassword) {
      state.password.error = "请填写当前密码、新密码和确认新密码。";
      renderPasswordChange();
      return;
    }
    if (newPassword.length < 12) {
      state.password.error = "新密码至少需要 12 位。";
      renderPasswordChange();
      return;
    }
    if (newPassword !== confirmPassword) {
      state.password.error = "两次输入的新密码不一致。";
      renderPasswordChange();
      return;
    }
    try {
      await api("/me/password", {
        method: "POST",
        body: JSON.stringify({
          current_password: currentPassword,
          new_password: newPassword,
          confirm_password: confirmPassword,
        }),
      });
      event.currentTarget.reset();
      state.password.success = "密码已更新，其他会话已退出。";
      renderPasswordChange();
    } catch (error) {
      state.password.error = error.message || "修改密码失败。";
      renderPasswordChange();
    }
  });
}
```

- [ ] **Step 5: Add minimal CSS**

In `internal/adminui/app.css`, add near `.user-panel button`:

```css
.user-actions {
  display: grid;
  gap: 8px;
}
```

Add near `.error`:

```css
.success {
  margin-top: 14px;
  color: #047857;
  font-weight: 650;
}
```

Add near other panel/form rules:

```css
.password-panel {
  max-width: 520px;
}

.stacked-form {
  display: grid;
}
```

- [ ] **Step 6: Run formatting-neutral checks**

Run:

```bash
go test ./internal/admin -count=1
```

Expected: PASS. There is no JS test harness in this repository; check syntax by loading the admin UI in Task 6.

- [ ] **Step 7: Commit Task 4**

Run:

```bash
git add internal/adminui/app.js internal/adminui/app.css
git commit -m "feat(adminui): add password change form"
```

---

### Task 5: Update Admin API Documentation

**Files:**
- Modify: `ARCHITECTURE.md`
- Inspect and maybe modify: `README.md`
- Inspect and maybe modify: `CLAUDE.md`

- [ ] **Step 1: Update `ARCHITECTURE.md` endpoint table**

In `ARCHITECTURE.md`, add this row after `/me`:

```markdown
| `/me/password` | POST | 已认证 + CSRF |
```

In the same admin section, update the security features sentence to include self-service password rotation:

```markdown
安全特性：HMAC 签名 Cookie、CSRF 防护、频率限制、bcrypt 密码哈希、自助密码轮换、全量审计日志。
```

- [ ] **Step 2: Check README and CLAUDE for relevant password notes**

Run:

```bash
rg -n "admin|password|密码|/admin/api|audit_users|admin/admin" README.md CLAUDE.md
```

Expected: The command prints any relevant lines. If it only prints unrelated quick-start text, leave both files unchanged.

- [ ] **Step 3: If README mentions the default admin password, add rotation guidance**

If `README.md` contains default admin credential guidance, add this sentence near that guidance:

```markdown
首次登录后请在管理后台左侧用户面板进入“修改密码”，完成本地 admin 密码轮换；成功后当前会话保留，其他会话会被撤销。
```

If `README.md` does not mention default admin credentials, do not edit it.

- [ ] **Step 4: If CLAUDE mentions admin endpoint lists, add `/me/password`**

If `CLAUDE.md` contains an admin endpoint table or endpoint list, add this entry:

```markdown
- `POST /admin/api/me/password` — 当前登录用户修改自己的本地密码，要求已认证和 CSRF。
```

If `CLAUDE.md` does not contain an admin endpoint list, do not edit it.

- [ ] **Step 5: Commit Task 5**

Run:

```bash
git add ARCHITECTURE.md README.md CLAUDE.md
git commit -m "docs: document admin password rotation"
```

If only `ARCHITECTURE.md` changed, use:

```bash
git add ARCHITECTURE.md
git commit -m "docs: document admin password rotation"
```

---

### Task 6: Verify Full Feature

**Files:**
- Verify all changed files from previous tasks.

- [ ] **Step 1: Run full Go tests**

Run:

```bash
make test
```

Expected: PASS for all Go packages.

- [ ] **Step 2: Run targeted admin tests again**

Run:

```bash
go test ./internal/admin -run 'Test(ChangeCurrentUserPassword|MiddlewareStoresSessionIDInPrincipal|RepositoryFindActiveUserByID|RepositoryUpdateUserPassword|RepositoryRevokeOtherSessions)' -count=1
```

Expected: PASS.

- [ ] **Step 3: Check git diff for password leaks**

Run:

```bash
git diff --stat HEAD~5..HEAD
git diff HEAD~5..HEAD -- internal/admin internal/adminui ARCHITECTURE.md README.md CLAUDE.md | rg -n "current_password|new_password|confirm_password|password_hash|PasswordHash|secret-password|new-secret-password|auditMetadata|password_changed"
```

Expected: The output only shows test fixtures, JSON field names, bcrypt hash fields, and the audit action name. It must not show logging of submitted passwords or audit metadata containing password values.

- [ ] **Step 4: Run the gateway locally for manual UI verification**

Run:

```bash
make run
```

Expected: The gateway starts and serves `/admin`. Keep this process running until the manual checks finish.

- [ ] **Step 5: Manual browser verification**

Open `http://localhost:8080/admin` and verify:

- Login still works with a valid local admin user.
- The left sidebar user panel shows `修改密码`.
- Submitting a short password shows `新密码至少需要 12 位。`.
- Submitting mismatched confirmation shows `两次输入的新密码不一致。`.
- Submitting a valid current password and matching new password shows `密码已更新，其他会话已退出。`.
- The page remains logged in after success.
- Logout still works.

- [ ] **Step 6: Final commit if Task 6 found any fixes**

If manual verification required fixes, commit them:

```bash
git add internal/admin internal/adminui ARCHITECTURE.md README.md CLAUDE.md
git commit -m "fix(admin): polish password change flow"
```

If no files changed during Task 6, do not create an empty commit.

---

## Completion Criteria

- `POST /admin/api/me/password` exists and is authenticated plus CSRF-protected.
- Any authenticated role can change its own password.
- Current password is required and verified.
- New password is at least 12 characters, matches confirmation, and differs from the current password.
- Current session remains valid.
- Other active sessions for the user are revoked.
- `password_changed` audit log is written without password material.
- Admin UI exposes a working self-service password form.
- `ARCHITECTURE.md` documents the endpoint.
- `make test` passes.
