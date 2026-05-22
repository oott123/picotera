package auth

import (
	"crypto/rand"
	"fmt"
	"regexp"

	"picotera/pkg/contract"
	"picotera/pkg/db"
)

// usernameRegex matches the strict username rule from
// specs/2026-05-23-auth-system/design.md §1: lowercase ASCII letters,
// digits, underscore, dash; 2-32 chars. No normalization performed —
// rejects strict per CLAUDE.md "fail fast on input".
var usernameRegex = regexp.MustCompile(`^[a-z0-9_-]{2,32}$`)

// ValidateUsername returns nil iff s matches the username regex.
// Returns *AuthError with code "invalid_username" on failure.
func ValidateUsername(s string) error {
	if !usernameRegex.MatchString(s) {
		return ErrInvalidUsername()
	}
	return nil
}

// ValidateDisplayName returns nil iff s is 1-128 characters long and
// contains no control characters (0x00-0x1f, 0x7f). No trimming or
// normalization.
func ValidateDisplayName(s string) error {
	if len(s) < 1 || len(s) > 128 {
		return ErrInvalidDisplayName()
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return ErrInvalidDisplayName()
		}
	}
	return nil
}

// GenerateUserHandle returns 64 cryptographically-random bytes intended
// for use as the WebAuthn user.id of a new account. Persisted in
// account.webauthn_user_handle.
func GenerateUserHandle() ([]byte, error) {
	b := make([]byte, 64)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("generate user handle: %w", err)
	}
	return b, nil
}

// Permits returns true iff the account is permitted to perform the action
// identified by p. Admins are unconditionally permitted; user-role accounts
// are checked against the matching boolean column.
func Permits(a *db.Account, p contract.Permission) bool {
	if a == nil {
		return false
	}
	if a.Role == "admin" {
		return true
	}
	switch p {
	case contract.PermViewOwnUsage:
		return a.CanViewOwnUsage
	case contract.PermManageOwnAPIKeys:
		return a.CanManageOwnApiKeys
	case contract.PermViewModels:
		return a.CanViewModels
	case contract.PermViewOwnTraces:
		return a.CanViewOwnTraces
	}
	return false
}

// PermissionsView projects an account's permission columns into the
// contract.Permissions wire shape. Admin accounts surface as all-true.
func PermissionsView(a *db.Account) contract.Permissions {
	if a == nil {
		return contract.Permissions{}
	}
	if a.Role == "admin" {
		return contract.Permissions{
			ViewOwnUsage:     true,
			ManageOwnAPIKeys: true,
			ViewModels:       true,
			ViewOwnTraces:    true,
		}
	}
	return contract.Permissions{
		ViewOwnUsage:     a.CanViewOwnUsage,
		ManageOwnAPIKeys: a.CanManageOwnApiKeys,
		ViewModels:       a.CanViewModels,
		ViewOwnTraces:    a.CanViewOwnTraces,
	}
}
