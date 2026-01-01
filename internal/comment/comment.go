// Package comment provides abstractions for publishing diff results to external systems.
package comment

import "context"

// Poster can publish a formatted diff comment to an upstream system.
type Poster interface {
	// Post publishes the given comment body. The context can be used for
	// cancellation and timeout control.
	Post(ctx context.Context, body string) error
}
