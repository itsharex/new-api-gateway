# Current User Password Change

## Problem

Admin UI users can log in and log out, but they cannot change their own local password from the management dashboard. This is especially risky while the seeded `admin/admin` account exists as an idempotent bootstrap path: operators need a first-class way to rotate that password without editing the database directly.

## Goals

- Let any authenticated admin UI user change their own password.
- Require the current password before accepting a new password.
- Require new passwords to be at least 12 characters and different from the current password.
- Keep the current session valid after a successful change.
- Revoke the same user's other active sessions after a successful change.
- Preserve existing CSRF, bcrypt hashing, session cookie, and audit log semantics.
- Avoid logging or returning plaintext passwords.

## Non-Goals

- No user-management screen for changing other users' passwords.
- No password reset flow, email flow, or external identity-provider integration.
- No schema change unless implementation discovers an existing table cannot support the operation.
- No role restriction beyond being authenticated; `viewer`, `auditor`, `raw_access`, and `admin` can all change their own password.

## Approach

Use the existing local admin account model. Add an authenticated, CSRF-protected personal password endpoint and a small UI entry from the logged-in user's panel.

Recommended endpoint:

```text
POST /admin/api/me/password
```

Request body:

```json
{
  "current_password": "...",
  "new_password": "...",
  "confirm_password": "..."
}
```

Successful response:

```text
204 No Content
```

## Backend Design

### Route

Register `POST /admin/api/me/password` in `internal/admin/handlers.go` with:

- `h.auth.Middleware`
- `h.requireCSRF`
- no RBAC `Require(...)` wrapper, because this is a self-service authenticated-user action

### Handler Flow

1. Decode JSON body.
2. Read the authenticated principal from context.
3. Reload the active user by `principal.UserID` so the handler has the current `password_hash`.
4. Verify `current_password` with `CheckPassword`.
5. Validate `new_password`:
   - at least 12 characters
   - equals `confirm_password`
   - does not match the current password
6. Hash `new_password` with `HashPassword`.
7. Update `audit_users.password_hash` and `updated_at`.
8. Revoke other active sessions for the same user, excluding the current session.
9. Insert an `audit_action_logs` row:
   - `action`: `password_changed`
   - `target_type`: `audit_user`
   - `target_id`: username
   - `metadata_json`: `{"revoked_other_sessions":true}`
10. Return `204 No Content`.

### Current Session Identity

The handler needs the current `session_id` to preserve that session while revoking the user's other sessions. Extend `Principal` with a `SessionID string` field tagged `json:"-"`, and set it in `Auth.Middleware` after cookie verification and `PrincipalBySession` lookup.

This keeps `/admin/api/me` responses unchanged while making the session id available to trusted server-side handlers.

### Repository Additions

Add focused methods to `internal/admin/repository.go`:

- `FindActiveUserByID(ctx, userID) (User, error)`
- `UpdateUserPassword(ctx, userID, passwordHash, now) error`
- `RevokeOtherSessions(ctx, userID, keepSessionID, now) error`

`RevokeOtherSessions` should only revoke sessions where:

- `user_id = $1`
- `session_id <> $2`
- `revoked_at IS NULL`
- `expires_at > $3`

## Frontend Design

Add a self-service "修改密码" entry in the left sidebar user panel in `internal/adminui/app.js`, near "退出登录". It should not be part of the admin-only "系统设置" view.

Clicking "修改密码" opens a simple main-area view with three password fields:

- 当前密码
- 新密码
- 确认新密码

Submit through the existing `api()` helper so the request uses same-origin credentials and the existing CSRF header behavior.

On success:

- show a short success message
- clear all password fields
- keep the user logged in

On failure:

- show the backend error message in the form
- keep the user on the password form

The frontend can pre-check length and confirmation, but the backend remains authoritative.

## Error Handling

Use clear, non-secret responses:

- invalid JSON: `400 invalid json`
- missing current password, new password, or confirmation: `400 password fields are required`
- new password shorter than 12 characters: `400 new password must be at least 12 characters`
- confirmation mismatch: `400 new password confirmation does not match`
- new password same as current password: `400 new password must be different from current password`
- wrong current password: `401 current password is incorrect`
- missing or invalid session: existing `401 Unauthorized`
- missing or invalid CSRF token: existing `403 Forbidden`
- repository or bcrypt failures: `500 failed to change password`

Do not include submitted passwords, password hashes, or password length in logs or audit metadata.

## Audit And Security

Password changes are audited with `password_changed`. Audit metadata records only that other sessions were revoked. The current session remains valid, and other active sessions for the same user are revoked immediately.

The implementation must continue using bcrypt for password hashes and must not persist plaintext passwords.

## Testing

### Handler Tests

- Successful password change returns `204`.
- The stored password hash changes.
- The old password no longer verifies, and the new password verifies.
- The current session remains valid for `/admin/api/me`.
- Other sessions for the same user are revoked.
- `password_changed` audit action is inserted.
- Wrong current password returns `401` and does not update the hash.
- New password shorter than 12 characters returns `400`.
- Confirmation mismatch returns `400`.
- New password equal to current password returns `400`.
- Missing or forged CSRF token returns `403`.

### Repository Tests

- `FindActiveUserByID` queries only active users by id.
- `UpdateUserPassword` updates `password_hash` and `updated_at` for the given user id.
- `RevokeOtherSessions` excludes the current session and only touches active, unrevoked, unexpired sessions.
- New repository methods return `ErrAdminDBRequired` when no DB is configured.

### Frontend Checks

- "修改密码" is visible for any logged-in role.
- Submitting valid values calls `/admin/api/me/password`.
- Success keeps the shell rendered and shows the success message.
- Failure keeps the form visible and displays the error.

## Documentation

Update `ARCHITECTURE.md` admin API documentation to include `POST /me/password` as an authenticated endpoint. Check `README.md` and `CLAUDE.md` for admin login/security notes and update them only if they contain endpoint lists or password-rotation guidance affected by this feature.

## Scope

This is a single feature touching:

- `internal/admin/models.go`
- `internal/admin/auth.go`
- `internal/admin/repository.go`
- `internal/admin/handlers.go`
- `internal/admin/*_test.go`
- `internal/adminui/app.js`
- `internal/adminui/app.css` if the form needs existing style reuse or a small spacing rule
- `ARCHITECTURE.md`, plus `README.md` or `CLAUDE.md` only if relevant

No migration is planned because `audit_users.password_hash`, `audit_users.updated_at`, `audit_sessions.revoked_at`, and audit logs already exist.
