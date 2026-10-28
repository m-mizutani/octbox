package interfaces

import (
	"errors"
	"fmt"
	"time"
)

var (
	// ErrSecretNotFound is returned by SecureStore.Get when the key has no
	// value.
	ErrSecretNotFound = errors.New("secret is not stored")

	// ErrSecureStoreUnavailable is returned by every SecureStore method on an
	// OS without a secure storage implementation.
	ErrSecureStoreUnavailable = errors.New("secure storage is not available on this OS")

	// ErrUnauthorized is returned when GitHub answers 401.
	ErrUnauthorized = errors.New("github rejected the token")

	// ErrDeviceCodeExpired is returned when the user did not enter the device
	// code before it expired.
	ErrDeviceCodeExpired = errors.New("device code expired")

	// ErrAccessDenied is returned when the user cancelled the authorization.
	ErrAccessDenied = errors.New("authorization was denied")

	// ErrDeviceFlowDisabled is returned when the OAuth app does not allow the
	// device flow.
	ErrDeviceFlowDisabled = errors.New("device flow is disabled for the OAuth app")

	// ErrItemNotFound is returned by the repository for an unknown item ID.
	ErrItemNotFound = errors.New("item not found")

	// ErrSchemaTooNew is returned when the database was written by a newer
	// binary.
	ErrSchemaTooNew = errors.New("database schema is newer than this binary")

	// ErrUnsupportedOS is returned by the browser on an OS without a known
	// command to open a URL.
	ErrUnsupportedOS = errors.New("opening a browser is not supported on this OS")
)

// RateLimitedError is returned when GitHub asks the client to wait.
type RateLimitedError struct {
	RetryAfter time.Duration
}

func (e *RateLimitedError) Error() string {
	return fmt.Sprintf("github rate limit exceeded; retry after %s", e.RetryAfter)
}

// APIError is returned for any other non-2xx response, and for a GraphQL
// response whose errors are not only NOT_FOUND or FORBIDDEN on single nodes
// (StatusCode 200).
type APIError struct {
	StatusCode int
}

func (e *APIError) Error() string {
	return fmt.Sprintf("github api error: status %d", e.StatusCode)
}
