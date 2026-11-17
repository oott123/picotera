package server

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"picotera/pkg/contract"
	"picotera/pkg/kv"

	"github.com/danielgtaylor/huma/v2"
)

func (s *Server) handleListKvEntries(ctx context.Context, in *contract.ListKvEntriesRequest) (*contract.ListKvEntriesResponse, error) {
	pattern := in.Pattern
	if pattern == "" {
		pattern = "*"
	}

	result, err := s.kvStore.ScanEntries(ctx, pattern, in.Cursor, 100)
	if err != nil {
		if errors.Is(err, kv.ErrInvalidArgument) {
			return nil, huma.Error400BadRequest(err.Error())
		}
		return nil, huma.Error500InternalServerError("failed to scan kv entries", err)
	}

	entries := make([]contract.KvEntryView, len(result.Entries))
	for i, e := range result.Entries {
		entries[i] = contract.KvEntryView{
			Key:   e.Key,
			Value: e.Value,
			TTL:   e.TTL,
		}
	}

	var nextCursorStr string
	if result.NextCursor != 0 {
		nextCursorStr = strconv.FormatUint(result.NextCursor, 10)
	}

	return &contract.ListKvEntriesResponse{
		Body: contract.PaginatedBody[contract.KvEntryView]{
			Items: entries,
			Pagination: contract.PaginationInfo{
				NextCursor: nextCursorStr,
				HasMore:    result.NextCursor != 0,
			},
		},
	}, nil
}

func (s *Server) handleGetKvEntry(ctx context.Context, in *contract.GetKvEntryRequest) (*contract.GetKvEntryResponse, error) {
	val, err := s.kvStore.Get(ctx, in.Key)
	if err != nil {
		if errors.Is(err, kv.ErrKeyNotFound) {
			return nil, huma.Error404NotFound("key not found")
		}
		return nil, huma.Error500InternalServerError("failed to get kv entry", err)
	}

	ttl, err := s.kvStore.TTL(ctx, in.Key)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to get ttl", err)
	}

	return &contract.GetKvEntryResponse{
		Body: contract.KvEntryView{
			Key:   in.Key,
			Value: val,
			TTL:   ttl,
		},
	}, nil
}

func (s *Server) handleUpsertKvEntry(ctx context.Context, in *contract.UpsertKvEntryRequest) (*contract.UpsertKvEntryResponse, error) {
	if in.Body.TTLSeconds != nil && *in.Body.TTLSeconds > 0 {
		ttl := time.Duration(*in.Body.TTLSeconds) * time.Second
		if err := s.kvStore.SetEx(ctx, in.Key, in.Body.Value, ttl); err != nil {
			return nil, huma.Error500InternalServerError("failed to set kv entry", err)
		}
	} else {
		if err := s.kvStore.Set(ctx, in.Key, in.Body.Value); err != nil {
			return nil, huma.Error500InternalServerError("failed to set kv entry", err)
		}
	}

	ttl, err := s.kvStore.TTL(ctx, in.Key)
	if err != nil {
		return nil, fmt.Errorf("failed to get ttl after set: %w", err)
	}

	return &contract.UpsertKvEntryResponse{
		Body: contract.KvEntryView{
			Key:   in.Key,
			Value: in.Body.Value,
			TTL:   ttl,
		},
	}, nil
}

func (s *Server) handleDeleteKvEntry(ctx context.Context, in *contract.DeleteKvEntryRequest) (*struct{}, error) {
	if err := s.kvStore.Del(ctx, in.Body.Key); err != nil {
		return nil, huma.Error500InternalServerError("failed to delete kv entry", err)
	}
	return &struct{}{}, nil
}
