package securestore

import "github.com/m-mizutani/octbox/pkg/domain/interfaces"

func NewKeyringStore() interfaces.SecureStore     { return &keyringStore{} }
func NewUnsupportedStore() interfaces.SecureStore { return &unsupportedStore{} }
