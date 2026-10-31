// Package usecasetest provides hand-written test doubles of the domain
// interfaces, shared by the tests of pkg/usecase, pkg/controller and
// pkg/cli. It is imported only by tests.
package usecasetest

import (
	"context"
	"sync"
	"time"

	"github.com/m-mizutani/octbox/pkg/domain/interfaces"
	"github.com/m-mizutani/octbox/pkg/domain/model"
)

// GitHubClient records every call. Each *Func field answers the call when
// set; otherwise the call returns an empty result.
type GitHubClient struct {
	mu sync.Mutex

	ViewerFunc            func(ctx context.Context) (*model.Viewer, error)
	SearchItemsFunc       func(ctx context.Context, query, after string) (*model.SearchPage, error)
	GetItemsFunc          func(ctx context.Context, ids []model.ItemID) (*model.ItemsResult, error)
	ResolveItemsFunc      func(ctx context.Context, refs []model.ItemRef) (*model.ItemsResult, error)
	ListNotificationsFunc func(ctx context.Context, since time.Time, lastModified string) (*model.NotificationPage, error)

	ViewerCalls       int
	SearchCalls       []SearchCall
	GetItemsCalls     [][]model.ItemID
	ResolveCalls      [][]model.ItemRef
	NotificationCalls []NotificationCall
}

type SearchCall struct {
	Query string
	After string
}

type NotificationCall struct {
	Since        time.Time
	LastModified string
}

var _ interfaces.GitHubClient = (*GitHubClient)(nil)

func (x *GitHubClient) Viewer(ctx context.Context) (*model.Viewer, error) {
	x.mu.Lock()
	x.ViewerCalls++
	x.mu.Unlock()
	if x.ViewerFunc != nil {
		return x.ViewerFunc(ctx)
	}
	return &model.Viewer{Login: "alice"}, nil
}

func (x *GitHubClient) SearchItems(ctx context.Context, query, after string) (*model.SearchPage, error) {
	x.mu.Lock()
	x.SearchCalls = append(x.SearchCalls, SearchCall{Query: query, After: after})
	x.mu.Unlock()
	if x.SearchItemsFunc != nil {
		return x.SearchItemsFunc(ctx, query, after)
	}
	return &model.SearchPage{RateLimit: model.RateLimit{Remaining: -1}}, nil
}

func (x *GitHubClient) GetItems(ctx context.Context, ids []model.ItemID) (*model.ItemsResult, error) {
	x.mu.Lock()
	x.GetItemsCalls = append(x.GetItemsCalls, append([]model.ItemID(nil), ids...))
	x.mu.Unlock()
	if x.GetItemsFunc != nil {
		return x.GetItemsFunc(ctx, ids)
	}
	return &model.ItemsResult{RateLimit: model.RateLimit{Remaining: -1}}, nil
}

func (x *GitHubClient) ResolveItems(ctx context.Context, refs []model.ItemRef) (*model.ItemsResult, error) {
	x.mu.Lock()
	x.ResolveCalls = append(x.ResolveCalls, append([]model.ItemRef(nil), refs...))
	x.mu.Unlock()
	if x.ResolveItemsFunc != nil {
		return x.ResolveItemsFunc(ctx, refs)
	}
	return &model.ItemsResult{RateLimit: model.RateLimit{Remaining: -1}}, nil
}

func (x *GitHubClient) ListNotifications(ctx context.Context, since time.Time, lastModified string) (*model.NotificationPage, error) {
	x.mu.Lock()
	x.NotificationCalls = append(x.NotificationCalls, NotificationCall{Since: since, LastModified: lastModified})
	x.mu.Unlock()
	if x.ListNotificationsFunc != nil {
		return x.ListNotificationsFunc(ctx, since, lastModified)
	}
	return &model.NotificationPage{NotModified: true}, nil
}

// Reset clears the recorded calls.
func (x *GitHubClient) Reset() {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.ViewerCalls = 0
	x.SearchCalls = nil
	x.GetItemsCalls = nil
	x.ResolveCalls = nil
	x.NotificationCalls = nil
}

// DeviceAuthenticator returns Code from StartDeviceAuth and Token or
// WaitErr from WaitDeviceToken.
type DeviceAuthenticator struct {
	Code       *model.DeviceCode
	StartErr   error
	Token      model.GitHubToken
	WaitErr    error
	StartCalls int
	WaitCalls  int
}

var _ interfaces.DeviceAuthenticator = (*DeviceAuthenticator)(nil)

func (x *DeviceAuthenticator) StartDeviceAuth(context.Context) (*model.DeviceCode, error) {
	x.StartCalls++
	if x.StartErr != nil {
		return nil, x.StartErr
	}
	if x.Code != nil {
		return x.Code, nil
	}
	return &model.DeviceCode{
		DeviceCode:      "dev-123",
		UserCode:        "ABCD-1234",
		VerificationURI: "https://github.com/login/device",
		ExpiresAt:       time.Now().Add(15 * time.Minute),
		Interval:        5 * time.Second,
	}, nil
}

func (x *DeviceAuthenticator) WaitDeviceToken(context.Context, *model.DeviceCode) (model.GitHubToken, error) {
	x.WaitCalls++
	if x.WaitErr != nil {
		return "", x.WaitErr
	}
	return x.Token, nil
}

// SecureStore keeps values in memory. GetErr, SetErr and DeleteErr, when
// set, are returned instead.
type SecureStore struct {
	mu        sync.Mutex
	Values    map[model.SecretKey]string
	GetErr    error
	SetErr    error
	DeleteErr error
}

var _ interfaces.SecureStore = (*SecureStore)(nil)

func (x *SecureStore) Get(_ context.Context, key model.SecretKey) (string, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.GetErr != nil {
		return "", x.GetErr
	}
	v, ok := x.Values[key]
	if !ok {
		return "", interfaces.ErrSecretNotFound
	}
	return v, nil
}

func (x *SecureStore) Set(_ context.Context, key model.SecretKey, value string) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.SetErr != nil {
		return x.SetErr
	}
	if x.Values == nil {
		x.Values = map[model.SecretKey]string{}
	}
	x.Values[key] = value
	return nil
}

func (x *SecureStore) Delete(_ context.Context, key model.SecretKey) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.DeleteErr != nil {
		return x.DeleteErr
	}
	delete(x.Values, key)
	return nil
}

// Browser records the opened URLs and returns Err.
type Browser struct {
	mu     sync.Mutex
	Opened []string
	Err    error
}

var _ interfaces.Browser = (*Browser)(nil)

func (x *Browser) Open(_ context.Context, url string) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.Opened = append(x.Opened, url)
	return x.Err
}

// UpdateIndicator records the calls as "register", "show_update" and
// "unregister", and returns the matching *Err.
type UpdateIndicator struct {
	mu            sync.Mutex
	Calls         []string
	RegisterErr   error
	ShowUpdateErr error
	UnregisterErr error
}

var _ interfaces.UpdateIndicator = (*UpdateIndicator)(nil)

func (x *UpdateIndicator) record(call string, err error) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.Calls = append(x.Calls, call)
	return err
}

func (x *UpdateIndicator) Register(context.Context) error {
	return x.record("register", x.RegisterErr)
}

func (x *UpdateIndicator) ShowUpdate(context.Context) error {
	return x.record("show_update", x.ShowUpdateErr)
}

func (x *UpdateIndicator) Unregister(context.Context) error {
	return x.record("unregister", x.UnregisterErr)
}

// UnreadCountDisplay records the counts shown and the removals.
// ShowErrs[i] is returned by the i-th ShowUnreadCount call, nil when absent.
type UnreadCountDisplay struct {
	mu       sync.Mutex
	Shown    []int
	Removed  int
	ShowErrs []error
	// OnShow, when set, runs after each ShowUnreadCount call is recorded.
	OnShow func(count int)
}

var _ interfaces.UnreadCountDisplay = (*UnreadCountDisplay)(nil)

func (x *UnreadCountDisplay) ShowUnreadCount(_ context.Context, count int) error {
	x.mu.Lock()
	i := len(x.Shown)
	x.Shown = append(x.Shown, count)
	var err error
	if i < len(x.ShowErrs) {
		err = x.ShowErrs[i]
	}
	onShow := x.OnShow
	x.mu.Unlock()
	if onShow != nil {
		onShow(count)
	}
	return err
}

func (x *UnreadCountDisplay) RemoveUnreadCount(context.Context) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.Removed++
	return nil
}

// ShownCounts returns a copy of the counts shown so far.
func (x *UnreadCountDisplay) ShownCounts() []int {
	x.mu.Lock()
	defer x.mu.Unlock()
	return append([]int(nil), x.Shown...)
}

// CallCount returns how many times call was recorded.
func (x *UpdateIndicator) CallCount(call string) int {
	x.mu.Lock()
	defer x.mu.Unlock()
	n := 0
	for _, c := range x.Calls {
		if c == call {
			n++
		}
	}
	return n
}
