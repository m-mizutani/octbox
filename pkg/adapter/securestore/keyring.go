//go:build darwin || linux

package securestore

import "github.com/m-mizutani/octbox/pkg/domain/interfaces"

func New() interfaces.SecureStore {
	return &keyringStore{}
}
