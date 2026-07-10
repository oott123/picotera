# Design: Pricing Match with Slash Stripping

## Problem

The current `Match` function in `pkg/pricing/match.go` computes Levenshtein distance between the user-supplied `target` string and a catalog model's `id` (and aliases) exactly as they appear. When either side contains a provider prefix separated by a slash (e.g. `openai/gpt-5.5`), the prefix dominates the edit distance and can prevent a good match against a bare model name such as `gpt-5.5`.

## Solution

For every `(target, candidate)` pair evaluated inside `Match`, compute up to four Levenshtein distances and use the minimum:

1. `levenshtein(target, candidate)` — original strings.
2. `levenshtein(stripLastSlash(target), candidate)` — stripped target.
3. `levenshtein(target, stripLastSlash(candidate))` — stripped candidate.
4. `levenshtein(stripLastSlash(target), stripLastSlash(candidate))` — both stripped.

`stripLastSlash(s)` returns the substring after the last `/` if one exists and there is non-empty content after it; otherwise it returns `s` unchanged. This prevents degenerate cases such as `openai/` from producing an empty string.

## Scope

- Modify only `pkg/pricing/match.go`.
- Add corresponding unit tests in `pkg/pricing/match_test.go`.
- No change to API contract, endpoint, or frontend.
