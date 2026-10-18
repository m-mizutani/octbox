package interfaces

import (
	"context"
	"time"

	"github.com/m-mizutani/octbox/pkg/domain/model"
)

type GitHubClient interface {
	Viewer(ctx context.Context) (*model.Viewer, error)
	// SearchItems runs one GraphQL search page (first: 50).
	SearchItems(ctx context.Context, query string, after string) (*model.SearchPage, error)
	// GetItems fetches up to 100 items by node ID.
	GetItems(ctx context.Context, ids []model.ItemID) (*model.ItemsResult, error)
	// ResolveItems fetches up to 50 items by repository and number.
	ResolveItems(ctx context.Context, refs []model.ItemRef) (*model.ItemsResult, error)
	// ListNotifications follows every page; NotModified is set on 304.
	ListNotifications(ctx context.Context, since time.Time, lastModified string) (*model.NotificationPage, error)
}

type GitHubClientFactory func(token model.GitHubToken) GitHubClient

type DeviceAuthenticator interface {
	StartDeviceAuth(ctx context.Context) (*model.DeviceCode, error)
	WaitDeviceToken(ctx context.Context, code *model.DeviceCode) (model.GitHubToken, error)
}
