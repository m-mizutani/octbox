// Package securestore implements interfaces.SecureStore for each OS. The
// implementation is chosen at build time: keyring.go (macOS, Linux) and
// unsupported.go (every other OS) each define New. Supporting another OS
// means adding a file with its build tag and removing that OS from the tag
// of unsupported.go.
package securestore

import (
	"context"
	"errors"

	"github.com/m-mizutani/goerr/v2"
	"github.com/zalando/go-keyring"

	"github.com/m-mizutani/octbox/pkg/domain/interfaces"
	"github.com/m-mizutani/octbox/pkg/domain/model"
)

// serviceName is the service of every secure storage entry; the key is the
// account name.
const serviceName = "octbox"

// keyringStore uses the macOS keychain (/usr/bin/security) or the Linux
// Secret Service (D-Bus) through go-keyring.
type keyringStore struct{}

func (x *keyringStore) Get(_ context.Context, key model.SecretKey) (string, error) {
	v, err := keyring.Get(serviceName, string(key))
	if errors.Is(err, keyring.ErrNotFound) {
		return "", goerr.Wrap(interfaces.ErrSecretNotFound, "no secret in the secure storage", goerr.V("key", key))
	}
	if err != nil {
		return "", goerr.Wrap(err, "failed to read the secure storage", goerr.V("key", key))
	}
	return v, nil
}

func (x *keyringStore) Set(_ context.Context, key model.SecretKey, value string) error {
	if err := keyring.Set(serviceName, string(key), value); err != nil {
		return goerr.Wrap(err, "failed to store the token in the OS secure storage", goerr.V("key", key))
	}
	return nil
}

func (x *keyringStore) Delete(_ context.Context, key model.SecretKey) error {
	err := keyring.Delete(serviceName, string(key))
	if err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return goerr.Wrap(err, "failed to delete from the secure storage", goerr.V("key", key))
	}
	return nil
}

// unsupportedStore is used on an OS without a secure storage implementation.
type unsupportedStore struct{}

func (x *unsupportedStore) Get(context.Context, model.SecretKey) (string, error) {
	return "", interfaces.ErrSecureStoreUnavailable
}

func (x *unsupportedStore) Set(context.Context, model.SecretKey, string) error {
	return interfaces.ErrSecureStoreUnavailable
}

func (x *unsupportedStore) Delete(context.Context, model.SecretKey) error {
	return interfaces.ErrSecureStoreUnavailable
}
