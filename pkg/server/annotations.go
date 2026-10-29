package server

import (
	"context"
	"errors"

	"picotera/pkg/annotations"
	"picotera/pkg/logx"

	"github.com/jackc/pgx/v5"
)

// fetchModelAnnotations reads the model row and decodes its annotations
// column. Missing rows yield an empty map (the rewriteModel hook still gets
// to run for unconfigured models). Decode errors are logged and yield an
// empty map; the hook should not crash on a malformed annotation blob.
func (s *Server) fetchModelAnnotations(ctx context.Context, modelName string) map[string]string {
	if modelName == "" {
		return map[string]string{}
	}
	row, err := s.queries.GetModelByName(ctx, modelName)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			logx.WithContext(ctx).WithError(err).WithField("model", modelName).Warn("model annotations lookup failed")
		}
		return map[string]string{}
	}
	out, err := annotations.Decode(row.Annotations)
	if err != nil {
		logx.WithContext(ctx).WithError(err).WithField("model", modelName).Warn("model annotations decode failed")
		return map[string]string{}
	}
	return out
}

// candidateAnnotationsBuilder pins the request-scoped layers (model, user, and
// apiKey) so the per-candidate loop only needs to feed in the (provider,
// mpe entry) pair. apiKey is the highest-priority layer and applies to
// every candidate produced under this request. Order: model < provider <
// entry < user < apiKey.
type candidateAnnotationsBuilder struct {
	modelAnno  map[string]string
	userAnno   map[string]string
	apiKeyAnno map[string]string
}

// newCandidateAnnotationsBuilder decodes modelAnnoRaw (JSONB bytes — may be
// nil/empty for "{}"-equivalent) and stashes the user + api-key layers. The
// returned builder is safe to reuse across every candidate produced under this
// request; callers must not mutate the returned model map directly because
// it's reused as the seed of every merge result.
func newCandidateAnnotationsBuilder(modelAnnoRaw []byte, userAnno, apiKeyAnno map[string]string) (*candidateAnnotationsBuilder, error) {
	model, err := annotations.Decode(modelAnnoRaw)
	if err != nil {
		return nil, err
	}
	if userAnno == nil {
		userAnno = map[string]string{}
	}
	if apiKeyAnno == nil {
		apiKeyAnno = map[string]string{}
	}
	return &candidateAnnotationsBuilder{modelAnno: model, userAnno: userAnno, apiKeyAnno: apiKeyAnno}, nil
}

// modelLayer returns the decoded model-layer annotation map. The returned
// value is shared with the builder; callers must not mutate it.
func (b *candidateAnnotationsBuilder) modelLayer() map[string]string {
	return b.modelAnno
}

// apiKeyLayer returns the api-key-layer annotation map. The returned value
// is shared with the builder; callers must not mutate it.
func (b *candidateAnnotationsBuilder) apiKeyLayer() map[string]string {
	return b.apiKeyAnno
}

// merge returns the per-candidate merged annotations for the given (provider,
// entry) layers in addition to the pinned model + user + apiKey layers. Order:
// model < provider < entry < user < apiKey, later wins.
//
// providerAnnoRaw is JSONB bytes (decoded once here so callers don't double
// decode); entryAnno is already decoded because the route SQL hands provider
// model entries back as JSON arrays we walk in Go.
func (b *candidateAnnotationsBuilder) merge(providerAnnoRaw []byte, entryAnno map[string]string) (
	merged, providerDecoded map[string]string,
) {
	provider, err := annotations.Decode(providerAnnoRaw)
	if err != nil {
		provider = map[string]string{}
	}
	merged = annotations.Merge(b.modelAnno, provider, entryAnno, b.userAnno, b.apiKeyAnno)
	return merged, provider
}
