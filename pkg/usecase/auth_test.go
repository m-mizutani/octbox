package usecase_test

import (
	"context"
	"errors"
	"testing"

	"github.com/m-mizutani/goerr/v2"
	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/octbox/pkg/domain/interfaces"
	"github.com/m-mizutani/octbox/pkg/domain/model"
	"github.com/m-mizutani/octbox/pkg/usecase"
	"github.com/m-mizutani/octbox/pkg/usecase/usecasetest"
)

type authFixture struct {
	device  *usecasetest.DeviceAuthenticator
	store   *usecasetest.SecureStore
	client  *usecasetest.GitHubClient
	tokens  []model.GitHubToken
	auth    *usecase.Auth
	prompts []*model.DeviceCode
}

func newAuthFixture(t *testing.T, repo interfaces.Repository) *authFixture {
	t.Helper()
	f := &authFixture{
		device: &usecasetest.DeviceAuthenticator{Token: "gho_new"},
		store:  &usecasetest.SecureStore{},
		client: &usecasetest.GitHubClient{},
	}
	f.auth = usecase.NewAuth(f.device, f.store, func(token model.GitHubToken) interfaces.GitHubClient {
		f.tokens = append(f.tokens, token)
		return f.client
	}, repo)
	return f
}

func (f *authFixture) prompt(code *model.DeviceCode) {
	f.prompts = append(f.prompts, code)
}

func TestAuthLogin(t *testing.T) {
	ctx := context.Background()

	t.Run("stores the verified token", func(t *testing.T) {
		repo, _ := newRepo(t)
		f := newAuthFixture(t, repo)
		viewer, err := f.auth.Login(ctx, f.prompt)
		gt.NoError(t, err).Required()
		gt.Equal(t, viewer.Login, "alice")
		gt.A(t, f.prompts).Length(1)
		gt.Equal(t, f.prompts[0].UserCode, "ABCD-1234")
		gt.A(t, f.tokens).Equal([]model.GitHubToken{"gho_new"})
		gt.Equal(t, f.client.ViewerCalls, 1)
		gt.Equal(t, f.store.Values[model.SecretKeyGitHubToken], "gho_new")
	})

	t.Run("rejected token is not stored", func(t *testing.T) {
		repo, _ := newRepo(t)
		f := newAuthFixture(t, repo)
		f.client.ViewerFunc = func(context.Context) (*model.Viewer, error) { return nil, interfaces.ErrUnauthorized }
		_, err := f.auth.Login(ctx, f.prompt)
		gt.Error(t, err).Is(interfaces.ErrUnauthorized)
		_, ok := f.store.Values[model.SecretKeyGitHubToken]
		gt.False(t, ok)
	})

	t.Run("another account clears local data", func(t *testing.T) {
		repo, _ := newRepo(t)
		gt.NoError(t, repo.SaveItems(ctx, []model.Item{prItem("PR_1", 1, t0)})).Required()
		gt.NoError(t, repo.PutSyncState(ctx, &model.SyncState{Viewer: "bob", RateRemaining: -1, SearchCursor: t0})).Required()

		f := newAuthFixture(t, repo)
		_, err := f.auth.Login(ctx, f.prompt)
		gt.NoError(t, err).Required()

		gt.M(t, entries(t, repo)).Length(0)
		st, err := repo.GetSyncState(ctx)
		gt.NoError(t, err).Required()
		gt.Equal(t, *st, *model.NewSyncState())
	})

	t.Run("same account keeps local data", func(t *testing.T) {
		repo, _ := newRepo(t)
		gt.NoError(t, repo.SaveItems(ctx, []model.Item{prItem("PR_1", 1, t0)})).Required()
		gt.NoError(t, repo.PutSyncState(ctx, &model.SyncState{Viewer: "alice", RateRemaining: -1, SearchCursor: t0})).Required()

		f := newAuthFixture(t, repo)
		_, err := f.auth.Login(ctx, f.prompt)
		gt.NoError(t, err).Required()
		gt.M(t, entries(t, repo)).Length(1)
	})

	t.Run("a database failure leaves the new token unstored", func(t *testing.T) {
		repo, _ := newRepo(t)
		gt.NoError(t, repo.Close())
		f := newAuthFixture(t, repo)
		_, err := f.auth.Login(ctx, f.prompt)
		gt.Error(t, err)
		gt.M(t, f.store.Values).Length(0)
	})

	t.Run("a secure storage failure is tagged", func(t *testing.T) {
		repo, _ := newRepo(t)
		f := newAuthFixture(t, repo)
		f.store.SetErr = errors.New("no secret service")
		_, err := f.auth.Login(ctx, f.prompt)
		gt.True(t, goerr.HasTag(err, usecase.TagTokenNotStored))
	})

	for _, wantErr := range []error{interfaces.ErrDeviceCodeExpired, interfaces.ErrAccessDenied} {
		t.Run(wantErr.Error(), func(t *testing.T) {
			repo, _ := newRepo(t)
			f := newAuthFixture(t, repo)
			f.device.WaitErr = wantErr
			_, err := f.auth.Login(ctx, f.prompt)
			gt.Error(t, err).Is(wantErr)
			gt.M(t, f.store.Values).Length(0)
		})
	}
}

func TestAuthEnsureToken(t *testing.T) {
	ctx := context.Background()

	t.Run("stored token", func(t *testing.T) {
		repo, _ := newRepo(t)
		f := newAuthFixture(t, repo)
		f.store.Values = map[model.SecretKey]string{model.SecretKeyGitHubToken: "gho_stored"}
		token, err := f.auth.EnsureToken(ctx, f.prompt)
		gt.NoError(t, err).Required()
		gt.Equal(t, token, model.GitHubToken("gho_stored"))
		gt.Equal(t, f.device.StartCalls, 0)
	})

	t.Run("no token runs the login", func(t *testing.T) {
		repo, _ := newRepo(t)
		f := newAuthFixture(t, repo)
		token, err := f.auth.EnsureToken(ctx, f.prompt)
		gt.NoError(t, err).Required()
		gt.Equal(t, token, model.GitHubToken("gho_new"))
		gt.Equal(t, f.device.StartCalls, 1)
		gt.Equal(t, f.store.Values[model.SecretKeyGitHubToken], "gho_new")
	})

	t.Run("unavailable store does not start the login", func(t *testing.T) {
		repo, _ := newRepo(t)
		f := newAuthFixture(t, repo)
		f.store.GetErr = interfaces.ErrSecureStoreUnavailable
		_, err := f.auth.EnsureToken(ctx, f.prompt)
		gt.Error(t, err).Is(interfaces.ErrSecureStoreUnavailable)
		gt.Equal(t, f.device.StartCalls, 0)
	})
}

func TestAuthLogout(t *testing.T) {
	ctx := context.Background()
	repo, _ := newRepo(t)
	gt.NoError(t, repo.SaveItems(ctx, []model.Item{prItem("PR_1", 1, t0)})).Required()
	f := newAuthFixture(t, repo)
	f.store.Values = map[model.SecretKey]string{model.SecretKeyGitHubToken: "gho_stored"}

	gt.NoError(t, f.auth.Logout(ctx)).Required()
	gt.M(t, f.store.Values).Length(0)
	gt.M(t, entries(t, repo)).Length(1)

	gt.NoError(t, f.auth.Logout(ctx))
}
