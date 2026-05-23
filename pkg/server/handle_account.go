package server

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"picotera/pkg/auth"
	"picotera/pkg/contract"
	"picotera/pkg/db"
)

// accountViewFromRow projects a ListAccountsRow into AccountView. The row
// carries the same columns as db.Account plus a pre-computed LastSignInAt.
func accountViewFromRow(r *db.ListAccountsRow) contract.AccountView {
	a := db.Account{
		ID:                  r.ID,
		Username:            r.Username,
		DisplayName:         r.DisplayName,
		WebauthnUserHandle:  r.WebauthnUserHandle,
		Role:                r.Role,
		CanViewOwnUsage:     r.CanViewOwnUsage,
		CanManageOwnApiKeys: r.CanManageOwnApiKeys,
		CanViewModels:       r.CanViewModels,
		CanViewOwnTraces:    r.CanViewOwnTraces,
		Disabled:            r.Disabled,
		CreatedAt:           r.CreatedAt,
		UpdatedAt:           r.UpdatedAt,
	}
	var lsi *time.Time
	if r.LastSignInAt.Valid {
		v := r.LastSignInAt.Time
		lsi = &v
	}
	return accountViewFromAccount(&a, lsi)
}

// accountViewFromAccount projects a db.Account into AccountView with an
// optional lastSignInAt (nil on single-row fetches that don't carry the
// credential subquery).
func accountViewFromAccount(a *db.Account, lastSignInAt *time.Time) contract.AccountView {
	return contract.AccountView{
		ID:           a.ID,
		Username:     a.Username,
		DisplayName:  a.DisplayName,
		Role:         a.Role,
		Permissions:  auth.PermissionsView(a),
		Disabled:     a.Disabled,
		CreatedAt:    a.CreatedAt.Time,
		UpdatedAt:    a.UpdatedAt.Time,
		LastSignInAt: lastSignInAt,
	}
}

// ----- GET /accounts ---------------------------------------------------------

type listAccountsOut struct {
	Body []contract.AccountView
}

func (s *Server) handleListAccounts(ctx context.Context, _ *struct{}) (*listAccountsOut, error) {
	rows, err := s.queries.ListAccounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("handleListAccounts: query: %w", err)
	}
	views := make([]contract.AccountView, 0, len(rows))
	for i := range rows {
		views = append(views, accountViewFromRow(&rows[i]))
	}
	return &listAccountsOut{Body: views}, nil
}

// ----- GET /accounts/{id} ----------------------------------------------------

type getAccountIn struct {
	ID int32 `path:"id"`
}

func (s *Server) handleGetAccount(ctx context.Context, in *getAccountIn) (*accountOut, error) {
	a, err := s.queries.GetAccountByID(ctx, in.ID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, authErrToHuma(auth.ErrAccountNotFound())
		}
		return nil, fmt.Errorf("handleGetAccount: query: %w", err)
	}
	return &accountOut{Body: accountViewFromAccount(&a, nil)}, nil
}

// ----- PUT /accounts/{id} ----------------------------------------------------

type updateAccountIn struct {
	ID   int32 `path:"id"`
	Body struct {
		// username is immutable; reject if the caller supplies any value.
		Username    string               `json:"username,omitempty"`
		DisplayName string               `json:"displayName"`
		Role        string               `json:"role"`
		Permissions contract.Permissions `json:"permissions"`
		Disabled    bool                 `json:"disabled"`
	}
}

func (s *Server) handleUpdateAccount(ctx context.Context, in *updateAccountIn) (*accountOut, error) {
	if in.Body.Username != "" {
		return nil, authErrToHuma(auth.ErrUsernameImmutable())
	}
	if err := auth.ValidateDisplayName(in.Body.DisplayName); err != nil {
		return nil, authErrToHuma(err)
	}

	tx, err := s.dbPool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("handleUpdateAccount: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	q := s.queries.WithTx(tx)

	current, err := q.GetAccountByID(ctx, in.ID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, authErrToHuma(auth.ErrAccountNotFound())
		}
		return nil, fmt.Errorf("handleUpdateAccount: load: %w", err)
	}

	// If this account currently contributes to the active-admin count and the
	// update would remove that contribution, enforce the last-admin invariant.
	demoting := current.Role == "admin" && in.Body.Role != "admin"
	disabling := !current.Disabled && in.Body.Disabled
	if (demoting || disabling) && current.Role == "admin" && !current.Disabled {
		n, err := q.CountActiveAdminsForUpdate(ctx)
		if err != nil {
			return nil, fmt.Errorf("handleUpdateAccount: count admins: %w", err)
		}
		if n <= 1 {
			return nil, authErrToHuma(auth.ErrLastAdmin())
		}
	}

	updated, err := q.UpdateAccount(ctx, db.UpdateAccountParams{
		ID:                  in.ID,
		DisplayName:         in.Body.DisplayName,
		Role:                in.Body.Role,
		CanViewOwnUsage:     in.Body.Permissions.ViewOwnUsage,
		CanManageOwnApiKeys: in.Body.Permissions.ManageOwnAPIKeys,
		CanViewModels:       in.Body.Permissions.ViewModels,
		CanViewOwnTraces:    in.Body.Permissions.ViewOwnTraces,
		Disabled:            in.Body.Disabled,
	})
	if err != nil {
		return nil, fmt.Errorf("handleUpdateAccount: update: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("handleUpdateAccount: commit: %w", err)
	}

	// Best-effort: kick sessions when an account is freshly disabled so active
	// browsers are signed out before their next session refresh window.
	if disabling {
		_, _ = s.sessionStore.RevokeAllForAccount(ctx, in.ID)
	}

	return &accountOut{Body: accountViewFromAccount(&updated, nil)}, nil
}

// ----- POST /accounts/delete -------------------------------------------------

type deleteAccountIn struct {
	Body struct {
		ID int32 `json:"id"`
	}
}

func (s *Server) handleDeleteAccount(ctx context.Context, in *deleteAccountIn) (*struct{}, error) {
	tx, err := s.dbPool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("handleDeleteAccount: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	q := s.queries.WithTx(tx)

	current, err := q.GetAccountByID(ctx, in.Body.ID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, authErrToHuma(auth.ErrAccountNotFound())
		}
		return nil, fmt.Errorf("handleDeleteAccount: load: %w", err)
	}

	// Deleting the only active admin would leave the system in an
	// unrecoverable state.
	if current.Role == "admin" && !current.Disabled {
		n, err := q.CountActiveAdminsForUpdate(ctx)
		if err != nil {
			return nil, fmt.Errorf("handleDeleteAccount: count admins: %w", err)
		}
		if n <= 1 {
			return nil, authErrToHuma(auth.ErrLastAdmin())
		}
	}

	if err := q.DeleteAccountByID(ctx, in.Body.ID); err != nil {
		return nil, fmt.Errorf("handleDeleteAccount: delete: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("handleDeleteAccount: commit: %w", err)
	}

	// Best-effort: active sessions for this account are now dangling; revoke
	// them so browsers are signed out immediately.
	_, _ = s.sessionStore.RevokeAllForAccount(ctx, in.Body.ID)

	return &struct{}{}, nil
}

// ----- POST /accounts/credentials/delete -------------------------------------

type deleteAccountCredentialIn struct {
	Body struct {
		AccountID    int32 `json:"accountId"`
		CredentialID int32 `json:"credentialId"`
	}
}

func (s *Server) handleDeleteAccountCredential(ctx context.Context, in *deleteAccountCredentialIn) (*struct{}, error) {
	// Verify the account exists.
	_, err := s.queries.GetAccountByID(ctx, in.Body.AccountID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, authErrToHuma(auth.ErrAccountNotFound())
		}
		return nil, fmt.Errorf("handleDeleteAccountCredential: load account: %w", err)
	}

	// Verify ownership: the credential must belong to the given account. The
	// delete query is already owner-scoped (WHERE id=$1 AND account_id=$2), but
	// it's :exec so a no-match is silent. Scan the list to surface 404 cleanly.
	creds, err := s.queries.ListCredentialsByAccount(ctx, in.Body.AccountID)
	if err != nil {
		return nil, fmt.Errorf("handleDeleteAccountCredential: list creds: %w", err)
	}
	found := false
	for _, c := range creds {
		if c.ID == in.Body.CredentialID {
			found = true
			break
		}
	}
	if !found {
		return nil, authErrToHuma(auth.ErrCredentialNotFound())
	}

	if err := s.queries.DeleteCredentialByID(ctx, db.DeleteCredentialByIDParams{
		ID:        in.Body.CredentialID,
		AccountID: in.Body.AccountID,
	}); err != nil {
		return nil, fmt.Errorf("handleDeleteAccountCredential: delete: %w", err)
	}

	return &struct{}{}, nil
}

// ----- POST /accounts/revoke-sessions ----------------------------------------

type revokeAccountSessionsIn struct {
	Body struct {
		ID int32 `json:"id"`
	}
}

type revokeAccountSessionsOut struct {
	Body struct {
		Revoked int `json:"revoked"`
	}
}

func (s *Server) handleRevokeAccountSessions(ctx context.Context, in *revokeAccountSessionsIn) (*revokeAccountSessionsOut, error) {
	// Ensure the account exists before attempting any revocation.
	_, err := s.queries.GetAccountByID(ctx, in.Body.ID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, authErrToHuma(auth.ErrAccountNotFound())
		}
		return nil, fmt.Errorf("handleRevokeAccountSessions: load: %w", err)
	}

	revoked, err := s.sessionStore.RevokeAllForAccount(ctx, in.Body.ID)
	if err != nil {
		return nil, fmt.Errorf("handleRevokeAccountSessions: revoke: %w", err)
	}

	out := &revokeAccountSessionsOut{}
	out.Body.Revoked = revoked
	return out, nil
}

// ----- POST /accounts/reissue-enrollment -------------------------------------

type reissueEnrollmentIn struct {
	Body struct {
		ID int32 `json:"id"`
	}
}

type enrollmentURLOut struct {
	Body contract.EnrollmentURLResponse
}

func (s *Server) handleReissueEnrollment(ctx context.Context, in *reissueEnrollmentIn) (*enrollmentURLOut, error) {
	// Ensure the account exists.
	_, err := s.queries.GetAccountByID(ctx, in.Body.ID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, authErrToHuma(auth.ErrAccountNotFound())
		}
		return nil, fmt.Errorf("handleReissueEnrollment: load: %w", err)
	}

	id := in.Body.ID
	token, expiresAt, err := auth.IssueEnrollment(ctx, s.queries, auth.IntentReset, &id, 0)
	if err != nil {
		return nil, fmt.Errorf("handleReissueEnrollment: issue: %w", err)
	}

	url := s.config.PublicOrigins[0] + "/enroll/" + token
	return &enrollmentURLOut{Body: contract.EnrollmentURLResponse{
		URL:       url,
		ExpiresAt: expiresAt,
	}}, nil
}

// ----- POST /invitations -----------------------------------------------------

type createInvitationIn struct {
	Body struct {
		Username    string               `json:"username"`
		DisplayName string               `json:"displayName"`
		Role        string               `json:"role"`
		Permissions contract.Permissions `json:"permissions"`
	}
}

type invitationOut struct {
	Body contract.InvitationResponse
}

func (s *Server) handleCreateInvitation(ctx context.Context, in *createInvitationIn) (*invitationOut, error) {
	if err := auth.ValidateUsername(in.Body.Username); err != nil {
		return nil, authErrToHuma(err)
	}
	if err := auth.ValidateDisplayName(in.Body.DisplayName); err != nil {
		return nil, authErrToHuma(err)
	}

	// Check for username uniqueness before opening the TX so the friendly error
	// surfaces before the DB constraint fires.
	_, err := s.queries.GetAccountByUsername(ctx, in.Body.Username)
	if err == nil {
		return nil, authErrToHuma(auth.ErrUsernameTaken())
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("handleCreateInvitation: check username: %w", err)
	}

	handle, err := auth.GenerateUserHandle()
	if err != nil {
		return nil, fmt.Errorf("handleCreateInvitation: user handle: %w", err)
	}

	tx, err := s.dbPool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("handleCreateInvitation: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	q := s.queries.WithTx(tx)

	newAccount, err := q.InsertAccount(ctx, db.InsertAccountParams{
		Username:            in.Body.Username,
		DisplayName:         in.Body.DisplayName,
		WebauthnUserHandle:  handle,
		Role:                in.Body.Role,
		CanViewOwnUsage:     in.Body.Permissions.ViewOwnUsage,
		CanManageOwnApiKeys: in.Body.Permissions.ManageOwnAPIKeys,
		CanViewModels:       in.Body.Permissions.ViewModels,
		CanViewOwnTraces:    in.Body.Permissions.ViewOwnTraces,
		Disabled:            false,
	})
	if err != nil {
		// The UNIQUE constraint on username fires here if there was a race
		// between the pre-check above and the insert.
		return nil, authErrToHuma(auth.ErrUsernameTaken())
	}

	token, expiresAt, err := auth.IssueEnrollment(ctx, q, auth.IntentInvite, &newAccount.ID, 0)
	if err != nil {
		return nil, fmt.Errorf("handleCreateInvitation: issue enrollment: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("handleCreateInvitation: commit: %w", err)
	}

	url := s.config.PublicOrigins[0] + "/enroll/" + token
	return &invitationOut{Body: contract.InvitationResponse{
		Account:   accountViewFromAccount(&newAccount, nil),
		URL:       url,
		ExpiresAt: expiresAt,
	}}, nil
}

// ----- shared output types ---------------------------------------------------

type accountOut struct {
	Body contract.AccountView
}
