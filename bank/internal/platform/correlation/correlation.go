// Package correlation carries the id that ties a request to its ledger rows,
// logs and chaos events (docs/architecture.md, section 4.2).
package correlation

import "context"

type key struct{}

// With returns a context carrying the correlation id.
func With(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, key{}, id)
}

// From returns the correlation id, or "" when the context carries none.
func From(ctx context.Context) string {
	id, _ := ctx.Value(key{}).(string)

	return id
}
