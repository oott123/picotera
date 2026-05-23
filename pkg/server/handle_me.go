package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sirupsen/logrus"

	"picotera/pkg/auth"
	"picotera/pkg/contract"
	"picotera/pkg/db"
	"picotera/pkg/logx"
)

// ----- GET /me ------------------------------------------------------------

type meOut struct {
	Body contract.SessionView
}

func (s *Server) handleGetMe(ctx context.Context, _ *struct{}) (*meOut, error) {
	sess := auth.SessionFromContext(ctx)
	if sess == nil {
		// LoadSession middleware should have attached one; if not, registerOp's
		// AuthSession requirement would have already rejected. Defensive.
		return nil, authErrToHuma(auth.ErrNoSession())
	}
	return &meOut{Body: sessionView(sess.Account)}, nil
}

// ----- GET /me/credentials -----------------------------------------------

type credentialsOut struct {
	Body []contract.CredentialView
}

func (s *Server) handleListMyCredentials(ctx context.Context, _ *struct{}) (*credentialsOut, error) {
	sess := auth.SessionFromContext(ctx)
	if sess == nil {
		return nil, authErrToHuma(auth.ErrNoSession())
	}
	rows, err := s.queries.ListCredentialsByAccount(ctx, sess.Account.ID)
	if err != nil {
		return nil, fmt.Errorf("list credentials: %w", err)
	}
	out := make([]contract.CredentialView, 0, len(rows))
	for _, c := range rows {
		out = append(out, credentialView(&c))
	}
	return &credentialsOut{Body: out}, nil
}

// credentialView projects a db.WebauthnCredential into the public-safe shape.
// Full CredentialID is never returned — only the last 4 chars of base64url for
// forensic display.
func credentialView(c *db.WebauthnCredential) contract.CredentialView {
	suffix := credentialIDSuffix(c.CredentialID)
	out := contract.CredentialView{
		ID:                 c.ID,
		CredentialIDSuffix: suffix,
		Transports:         append([]string(nil), c.Transports...),
		BackupState:        c.BackupState,
		AttestationType:    c.AttestationType,
		CreatedAt:          c.CreatedAt.Time,
	}
	if c.Nickname.Valid {
		v := c.Nickname.String
		out.Nickname = &v
	}
	if c.LastUsedAt.Valid {
		t := c.LastUsedAt.Time
		out.LastUsedAt = &t
	}
	return out
}

func credentialIDSuffix(credID []byte) string {
	enc := base64.RawURLEncoding.EncodeToString(credID)
	if len(enc) <= 4 {
		return enc
	}
	return enc[len(enc)-4:]
}

// ----- POST /me/credentials/register/begin (raw chi) ---------------------

func (s *Server) handleAddCredentialBeginHTTP(w http.ResponseWriter, r *http.Request) {
	sess := auth.SessionFromContext(r.Context())
	if sess == nil {
		writeAuthErr(w, auth.ErrNoSession())
		return
	}

	creds, err := s.queries.ListCredentialsByAccount(r.Context(), sess.Account.ID)
	if err != nil {
		writeAuthErr(w, fmt.Errorf("add credential/begin: list: %w", err))
		return
	}
	exclude := make([]protocol.CredentialDescriptor, 0, len(creds))
	for _, c := range creds {
		exclude = append(exclude, protocol.CredentialDescriptor{
			Type:         protocol.PublicKeyCredentialType,
			CredentialID: c.CredentialID,
		})
	}
	wu := &auth.WebAuthnAccount{Account: sess.Account, Credentials: creds}

	creation, sessionData, err := s.webauthn.BeginRegistration(wu, auth.RegistrationOptions(exclude)...)
	if err != nil {
		writeAuthErr(w, auth.ErrWebAuthnCeremony(err.Error()))
		return
	}
	payload, err := json.Marshal(sessionData)
	if err != nil {
		writeAuthErr(w, fmt.Errorf("add credential/begin: marshal: %w", err))
		return
	}
	if err := s.kvStore.SetEx(r.Context(), "webauthn_ceremony:add:"+sess.Token, string(payload), 5*time.Minute); err != nil {
		writeAuthErr(w, fmt.Errorf("add credential/begin: setex: %w", err))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(creation.Response)
}

// ----- POST /me/credentials/register/complete (raw chi) ------------------

func (s *Server) handleAddCredentialCompleteHTTP(w http.ResponseWriter, r *http.Request) {
	sess := auth.SessionFromContext(r.Context())
	if sess == nil {
		writeAuthErr(w, auth.ErrNoSession())
		return
	}

	rawStash, err := s.kvStore.Get(r.Context(), "webauthn_ceremony:add:"+sess.Token)
	if err != nil {
		writeAuthErr(w, auth.ErrWebAuthnCeremony("ceremony expired"))
		return
	}
	var sessionData webauthn.SessionData
	if err := json.Unmarshal([]byte(rawStash), &sessionData); err != nil {
		writeAuthErr(w, auth.ErrWebAuthnCeremony("ceremony corrupt"))
		return
	}

	nickname := r.URL.Query().Get("nickname")

	parsed, err := protocol.ParseCredentialCreationResponseBody(r.Body)
	if err != nil {
		writeAuthErr(w, auth.ErrWebAuthnCeremony(err.Error()))
		return
	}

	// Refresh credentials list for the user adapter — between /begin and
	// /complete, no new credentials should have appeared, but stay correct.
	existing, _ := s.queries.ListCredentialsByAccount(r.Context(), sess.Account.ID)
	wu := &auth.WebAuthnAccount{Account: sess.Account, Credentials: existing}
	cred, err := s.webauthn.CreateCredential(wu, sessionData, parsed)
	if err != nil {
		writeAuthErr(w, auth.ErrWebAuthnCeremony(err.Error()))
		return
	}

	transports := make([]string, 0, len(cred.Transport))
	for _, t := range cred.Transport {
		transports = append(transports, string(t))
	}
	row, err := s.queries.InsertCredential(r.Context(), db.InsertCredentialParams{
		AccountID:       sess.Account.ID,
		CredentialID:    cred.ID,
		PublicKey:       cred.PublicKey,
		SignCount:       int64(cred.Authenticator.SignCount),
		Transports:      transports,
		Aaguid:          cred.Authenticator.AAGUID,
		AttestationType: cred.AttestationType,
		BackupEligible:  cred.Flags.BackupEligible,
		BackupState:     cred.Flags.BackupState,
		Nickname:        nicknameParam(nickname),
	})
	if err != nil {
		writeAuthErr(w, fmt.Errorf("add credential/complete: insert: %w", err))
		return
	}

	// Best-effort cleanup of the ceremony stash.
	_ = s.kvStore.Del(r.Context(), "webauthn_ceremony:add:"+sess.Token)

	logx.WithContext(r.Context()).WithFields(logrus.Fields{
		"event":      "auth.credential_added",
		"account_id": sess.Account.ID,
	}).Info("auth")

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(credentialView(&row))
}

// nicknameParam converts a possibly-empty string to a pgtype.Text suitable
// for InsertCredentialParams (NULL when empty).
func nicknameParam(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: s, Valid: true}
}

// ----- POST /me/credentials/delete ---------------------------------------

type deleteMyCredentialIn struct {
	Body struct {
		ID int32 `json:"id"`
	}
}

type emptyOut struct{}

func (s *Server) handleDeleteMyCredential(ctx context.Context, in *deleteMyCredentialIn) (*emptyOut, error) {
	sess := auth.SessionFromContext(ctx)
	if sess == nil {
		return nil, authErrToHuma(auth.ErrNoSession())
	}
	count, err := s.queries.CountCredentialsByAccount(ctx, sess.Account.ID)
	if err != nil {
		return nil, fmt.Errorf("count credentials: %w", err)
	}
	if count <= 1 {
		return nil, authErrToHuma(auth.ErrLastPasskey())
	}
	if err := s.queries.DeleteCredentialByID(ctx, db.DeleteCredentialByIDParams{
		ID:        in.Body.ID,
		AccountID: sess.Account.ID,
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, authErrToHuma(auth.ErrCredentialNotFound())
		}
		return nil, fmt.Errorf("delete credential: %w", err)
	}

	logx.WithContext(ctx).WithFields(logrus.Fields{
		"event":         "auth.credential_revoked_self",
		"account_id":    sess.Account.ID,
		"credential_id": in.Body.ID,
	}).Info("auth")

	return &emptyOut{}, nil
}
