package interfaces

import "context"

// UpdateIndicator shows, outside the inbox screen, that the inbox has
// received updates the user has not viewed. The host of the indicator
// decides when the user has viewed them.
type UpdateIndicator interface {
	// Register shows that octbox is running, with no update to show.
	Register(ctx context.Context) error
	// ShowUpdate shows that new updates have arrived.
	ShowUpdate(ctx context.Context) error
	// Unregister removes what Register showed.
	Unregister(ctx context.Context) error
}
