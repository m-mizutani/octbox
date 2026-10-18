package interfaces

import "context"

type Browser interface {
	Open(ctx context.Context, url string) error
}
