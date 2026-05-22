package auth

import (
	"context"
	"testing"

	"picotera/pkg/contract"
	"picotera/pkg/db"
)

func TestCheck_Public(t *testing.T) {
	if err := Check(nil, contract.AuthRequirement{Kind: contract.AuthPublic}); err != nil {
		t.Errorf("public should pass with nil session, got %v", err)
	}
}

func TestCheck_Session_NilFails(t *testing.T) {
	err := Check(nil, contract.AuthRequirement{Kind: contract.AuthSession})
	if AsAuthError(err) == nil || AsAuthError(err).Code != "no_session" {
		t.Errorf("want no_session, got %v", err)
	}
}

func TestCheck_Session_OKWithSession(t *testing.T) {
	s := &Session{Account: &db.Account{Role: "user"}}
	if err := Check(s, contract.AuthRequirement{Kind: contract.AuthSession}); err != nil {
		t.Errorf("session-only requirement should pass with any session, got %v", err)
	}
}

func TestCheck_Admin(t *testing.T) {
	req := contract.AuthRequirement{Kind: contract.AuthAdmin}
	// admin passes
	if err := Check(&Session{Account: &db.Account{Role: "admin"}}, req); err != nil {
		t.Errorf("admin should pass, got %v", err)
	}
	// user fails
	err := Check(&Session{Account: &db.Account{Role: "user"}}, req)
	if AsAuthError(err) == nil || AsAuthError(err).Code != "not_admin" {
		t.Errorf("want not_admin, got %v", err)
	}
	// nil fails
	err = Check(nil, req)
	if AsAuthError(err) == nil || AsAuthError(err).Code != "no_session" {
		t.Errorf("nil session should be no_session, got %v", err)
	}
}

func TestCheck_Permission(t *testing.T) {
	req := contract.RequirePermission(contract.PermViewOwnUsage)
	// admin auto-passes
	if err := Check(&Session{Account: &db.Account{Role: "admin"}}, req); err != nil {
		t.Errorf("admin should auto-pass permissions, got %v", err)
	}
	// user with the perm passes
	if err := Check(&Session{Account: &db.Account{Role: "user", CanViewOwnUsage: true}}, req); err != nil {
		t.Errorf("user with perm should pass, got %v", err)
	}
	// user without it fails with permission_denied
	err := Check(&Session{Account: &db.Account{Role: "user"}}, req)
	if AsAuthError(err) == nil || AsAuthError(err).Code != "permission_denied" {
		t.Errorf("want permission_denied, got %v", err)
	}
}

func TestSessionContext_Roundtrip(t *testing.T) {
	want := &Session{Account: &db.Account{ID: 7}}
	ctx := WithSession(context.Background(), want)
	got := SessionFromContext(ctx)
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if SessionFromContext(context.Background()) != nil {
		t.Error("empty ctx should return nil")
	}
}
