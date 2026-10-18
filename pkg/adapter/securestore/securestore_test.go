package securestore_test

import (
	"context"
	"testing"

	"github.com/m-mizutani/gt"
	"github.com/zalando/go-keyring"

	"github.com/m-mizutani/octbox/pkg/adapter/securestore"
	"github.com/m-mizutani/octbox/pkg/domain/interfaces"
	"github.com/m-mizutani/octbox/pkg/domain/model"
)

func TestKeyringStore(t *testing.T) {
	// MockInit replaces the OS keyring with go-keyring's in-memory provider.
	keyring.MockInit()
	ctx := context.Background()
	store := securestore.NewKeyringStore()

	_, err := store.Get(ctx, model.SecretKeyGitHubToken)
	gt.Error(t, err).Is(interfaces.ErrSecretNotFound)
	gt.NoError(t, store.Delete(ctx, model.SecretKeyGitHubToken))

	gt.NoError(t, store.Set(ctx, model.SecretKeyGitHubToken, "gho_abc")).Required()
	v, err := store.Get(ctx, model.SecretKeyGitHubToken)
	gt.NoError(t, err).Required()
	gt.Equal(t, v, "gho_abc")

	gt.NoError(t, store.Delete(ctx, model.SecretKeyGitHubToken)).Required()
	_, err = store.Get(ctx, model.SecretKeyGitHubToken)
	gt.Error(t, err).Is(interfaces.ErrSecretNotFound)
}

func TestUnsupportedStore(t *testing.T) {
	ctx := context.Background()
	store := securestore.NewUnsupportedStore()

	_, err := store.Get(ctx, model.SecretKeyGitHubToken)
	gt.Error(t, err).Is(interfaces.ErrSecureStoreUnavailable)
	gt.Error(t, store.Set(ctx, model.SecretKeyGitHubToken, "x")).Is(interfaces.ErrSecureStoreUnavailable)
	gt.Error(t, store.Delete(ctx, model.SecretKeyGitHubToken)).Is(interfaces.ErrSecureStoreUnavailable)
}
