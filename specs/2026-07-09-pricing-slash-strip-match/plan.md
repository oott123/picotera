# Plan: Pricing Match with Slash Stripping

## Step 1 — Implement score calculation with slash stripping

File: `pkg/pricing/match.go`

Add a small helper:

```go
func stripLastSlash(s string) string {
    if i := strings.LastIndex(s, "/"); i >= 0 && i+1 < len(s) {
        return s[i+1:]
    }
    return s
}
```

In `Match`, replace the current per-model score block:

```go
score := levenshtein.ComputeDistance(target, m.ID)
for _, alias := range m.Aliases {
    if d := levenshtein.ComputeDistance(target, alias); d < score {
        score = d
    }
}
```

with a helper that evaluates all four combinations (original, strip-target, strip-candidate, strip-both) for each candidate string (`m.ID` and every alias), keeping the smallest distance.

## Step 2 — Add unit tests

File: `pkg/pricing/match_test.go`

Add test cases covering:

- `target` with slash matched against bare catalog ID.
- Bare `target` matched against catalog ID with slash.
- Both sides with slash resolving to the same suffix.
- Regression: strings without slashes continue to behave exactly as before.
- Edge case: trailing slash (`openai/`) does not get stripped to empty string.

## Step 3 — Verify

Run:

```bash
go test ./pkg/pricing/...
```

All tests must pass.
