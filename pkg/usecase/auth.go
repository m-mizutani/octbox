package usecase

import (
	"context"
	"errors"

	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/octbox/pkg/domain/interfaces"
	"github.com/m-mizutani/octbox/pkg/domain/model"
)

// TagTokenNotStored marks a login that got a valid token but could not save
// it in the OS secure storage.
var TagTokenNotStored = goerr.NewTag("token_not_stored")

// Auth is the only component that reads, writes, or deletes the GitHub
// token.
type Auth struct {
	device    interfaces.DeviceAuthenticator
	store     interfaces.SecureStore
	newClient interfaces.GitHubClientFactory
	repo      interfaces.Repository
}

func NewAuth(device interfaces.DeviceAuthenticator, store interfaces.SecureStore,
	newClient interfaces.GitHubClientFactory, repo interfaces.Repository) *Auth {
	return &Auth{device: device, store: store, newClient: newClient, repo: repo}
}

// Login runs the device flow, checks the token, stores it, and clears the
// local data when another account was logged in before.
func (x *Auth) Login(ctx context.Context, prompt func(code *model.DeviceCode)) (*model.Viewer, error) {
	viewer, _, err := x.login(ctx, prompt)
	return viewer, err
}

// EnsureToken returns the stored token, running Login first when none is stored.
func (x *Auth) EnsureToken(ctx context.Context, prompt func(code *model.DeviceCode)) (model.GitHubToken, error) {
	stored, err := x.store.Get(ctx, model.SecretKeyGitHubToken)
	if err == nil && stored != "" {
		return model.GitHubToken(stored), nil
	}
	if err != nil && !errors.Is(err, interfaces.ErrSecretNotFound) {
		return "", goerr.Wrap(err, "failed to read the stored token")
	}

	_, token, err := x.login(ctx, prompt)
	if err != nil {
		return "", err
	}
	return token, nil
}

func (x *Auth) login(ctx context.Context, prompt func(code *model.DeviceCode)) (*model.Viewer, model.GitHubToken, error) {
	code, err := x.device.StartDeviceAuth(ctx)
	if err != nil {
		return nil, "", goerr.Wrap(err, "failed to start the login")
	}
	prompt(code)

	token, err := x.device.WaitDeviceToken(ctx, code)
	if err != nil {
		return nil, "", goerr.Wrap(err, "failed to log in")
	}

	viewer, err := x.newClient(token).Viewer(ctx)
	if err != nil {
		return nil, "", goerr.Wrap(err, "failed to verify the new token")
	}

	// The previous account's data is cleared before the new token is stored,
	// so a failed reset never leaves the new token next to the old data.
	state, err := x.repo.GetSyncState(ctx)
	if err != nil {
		return nil, "", goerr.Wrap(err, "failed to read the sync state")
	}
	if state.Viewer != "" && state.Viewer != viewer.Login {
		if err := x.repo.ResetAll(ctx); err != nil {
			return nil, "", goerr.Wrap(err, "failed to clear the data of the previous account",
				goerr.V("previous", state.Viewer), goerr.V("current", viewer.Login))
		}
	}

	if err := x.store.Set(ctx, model.SecretKeyGitHubToken, string(token)); err != nil {
		return nil, "", goerr.Wrap(err, "failed to store the token", goerr.T(TagTokenNotStored))
	}
	return viewer, token, nil
}

// Logout deletes the stored token and keeps the database.
func (x *Auth) Logout(ctx context.Context) error {
	if err := x.store.Delete(ctx, model.SecretKeyGitHubToken); err != nil {
		return goerr.Wrap(err, "failed to delete the token")
	}
	return nil
}
