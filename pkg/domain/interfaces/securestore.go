package interfaces

import (
	"context"

	"github.com/m-mizutani/octbox/pkg/domain/model"
)

// SecureStore keeps secrets in the storage the OS provides for them.
// Each OS gets its own implementation in pkg/adapter/securestore.
type SecureStore interface {
	Get(ctx context.Context, key model.SecretKey) (string, error) // ErrSecretNotFound
	Set(ctx context.Context, key model.SecretKey, value string) error
	Delete(ctx context.Context, key model.SecretKey) error // nil when the key is absent
}
