package github

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/m-mizutani/goerr/v2"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/endpoints"

	"github.com/m-mizutani/octbox/pkg/domain/interfaces"
	"github.com/m-mizutani/octbox/pkg/domain/model"
)

// DeviceAuthenticator runs the OAuth device flow of an OAuth app.
type DeviceAuthenticator struct {
	config     oauth2.Config
	httpClient *http.Client
}

var _ interfaces.DeviceAuthenticator = (*DeviceAuthenticator)(nil)

type DeviceOption func(*DeviceAuthenticator)

// WithEndpoint replaces the github.com endpoints, for tests.
func WithEndpoint(ep oauth2.Endpoint) DeviceOption {
	return func(x *DeviceAuthenticator) { x.config.Endpoint = ep }
}

func NewDeviceAuthenticator(clientID string, opts ...DeviceOption) *DeviceAuthenticator {
	x := &DeviceAuthenticator{
		config: oauth2.Config{
			ClientID: clientID,
			Endpoint: endpoints.GitHub,
			// repo is needed to search and read notifications of private
			// repositories; OAuth apps have no read-only scope for them.
			Scopes: []string{"repo"},
		},
		httpClient: http.DefaultClient,
	}
	for _, opt := range opts {
		opt(x)
	}
	return x
}

func (x *DeviceAuthenticator) withClient(ctx context.Context) context.Context {
	return context.WithValue(ctx, oauth2.HTTPClient, x.httpClient)
}

func (x *DeviceAuthenticator) StartDeviceAuth(ctx context.Context) (*model.DeviceCode, error) {
	resp, err := x.config.DeviceAuth(x.withClient(ctx))
	if err != nil {
		return nil, goerr.Wrap(convertDeviceError(err), "failed to start the device flow")
	}
	return &model.DeviceCode{
		DeviceCode:      resp.DeviceCode,
		UserCode:        resp.UserCode,
		VerificationURI: resp.VerificationURI,
		ExpiresAt:       resp.Expiry,
		Interval:        time.Duration(resp.Interval) * time.Second,
	}, nil
}

func (x *DeviceAuthenticator) WaitDeviceToken(ctx context.Context, code *model.DeviceCode) (model.GitHubToken, error) {
	da := &oauth2.DeviceAuthResponse{
		DeviceCode:      code.DeviceCode,
		UserCode:        code.UserCode,
		VerificationURI: code.VerificationURI,
		Expiry:          code.ExpiresAt,
		Interval:        int64(code.Interval / time.Second),
	}
	token, err := x.config.DeviceAccessToken(x.withClient(ctx), da)
	if err != nil {
		// DeviceAccessToken ends with DeadlineExceeded when the code expires
		// before GitHub reports expired_token.
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			return "", goerr.Wrap(interfaces.ErrDeviceCodeExpired, "the device code expired")
		}
		return "", goerr.Wrap(convertDeviceError(err), "failed to get a token by the device flow")
	}
	if token.AccessToken == "" {
		return "", goerr.New("GitHub returned an empty access token")
	}
	return model.GitHubToken(token.AccessToken), nil
}

func convertDeviceError(err error) error {
	var re *oauth2.RetrieveError
	if !errors.As(err, &re) {
		return err
	}
	switch re.ErrorCode {
	case "expired_token":
		return interfaces.ErrDeviceCodeExpired
	case "access_denied":
		return interfaces.ErrAccessDenied
	case "device_flow_disabled":
		return interfaces.ErrDeviceFlowDisabled
	}
	return goerr.Wrap(err, "OAuth error", goerr.V("error_code", re.ErrorCode))
}
