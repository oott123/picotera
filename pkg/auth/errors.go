package auth

import (
	"errors"
	"fmt"
	"net/http"
)

// AuthError is the canonical error type returned from auth checks and handlers.
// HTTP status, machine-readable code, and human message are all surfaced together;
// the registerOp helper and HTTP error writers project these into wire responses.
type AuthError struct {
	Status  int
	Code    string
	Message string
}

func (e *AuthError) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

func newErr(status int, code, message string) *AuthError {
	return &AuthError{Status: status, Code: code, Message: message}
}

// Constructors return fresh values so callers can decorate / re-wrap without
// sharing state. Match the catalogue in
// specs/2026-05-23-auth-system/design.md §8.

func ErrNoSession() *AuthError {
	return newErr(http.StatusUnauthorized, "no_session", "no session")
}

func ErrNotAdmin() *AuthError {
	return newErr(http.StatusForbidden, "not_admin", "admin required")
}

func ErrPermissionDenied() *AuthError {
	return newErr(http.StatusForbidden, "permission_denied", "permission denied")
}

func ErrAccountDisabled() *AuthError {
	return newErr(http.StatusForbidden, "account_disabled", "account disabled")
}

func ErrLastAdmin() *AuthError {
	return newErr(http.StatusConflict, "last_admin", "cannot demote, disable, or delete the only admin")
}

func ErrUsernameTaken() *AuthError {
	return newErr(http.StatusConflict, "username_taken", "username already exists")
}

func ErrEnrollmentExpired() *AuthError {
	return newErr(http.StatusGone, "enrollment_expired", "enrollment link expired")
}

func ErrEnrollmentConsumed() *AuthError {
	return newErr(http.StatusGone, "enrollment_consumed", "enrollment link already used")
}

func ErrInvalidUsername() *AuthError {
	return newErr(http.StatusBadRequest, "invalid_username", "username must match ^[a-z0-9_-]{2,32}$")
}

func ErrInvalidDisplayName() *AuthError {
	return newErr(http.StatusBadRequest, "invalid_display_name", "display name must be 1-128 chars with no control characters")
}

func ErrUsernameImmutable() *AuthError {
	return newErr(http.StatusBadRequest, "username_immutable", "username cannot be changed")
}

func ErrLastPasskey() *AuthError {
	return newErr(http.StatusBadRequest, "last_passkey", "cannot delete the only passkey on an account")
}

func ErrWebAuthnCeremony(detail string) *AuthError {
	return newErr(http.StatusBadRequest, "webauthn_ceremony_failed", "webauthn ceremony failed: "+detail)
}

func ErrAccountNotFound() *AuthError {
	return newErr(http.StatusNotFound, "account_not_found", "account not found")
}

func ErrCredentialNotFound() *AuthError {
	return newErr(http.StatusNotFound, "credential_not_found", "credential not found")
}

func ErrNotBootstrapped() *AuthError {
	return newErr(http.StatusServiceUnavailable, "not_bootstrapped", "no admin enrolled; run `picotera enroll-admin`")
}

// AsAuthError unwraps an error chain and returns the embedded *AuthError if any,
// or nil otherwise. Useful for handler error mapping.
func AsAuthError(err error) *AuthError {
	var a *AuthError
	if errors.As(err, &a) {
		return a
	}
	return nil
}
