package github_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/m-mizutani/gt"
	"golang.org/x/oauth2"

	"github.com/m-mizutani/octbox/pkg/adapter/github"
	"github.com/m-mizutani/octbox/pkg/domain/interfaces"
	"github.com/m-mizutani/octbox/pkg/domain/model"
)

// newDeviceServer returns an authenticator against a fake GitHub that issues
// a device code and answers each token poll with tokenResponses in order
// (the last one repeats).
func newDeviceServer(t *testing.T, tokenResponses ...string) (*github.DeviceAuthenticator, *atomic.Int32) {
	t.Helper()
	var polls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/login/device/code", func(w http.ResponseWriter, r *http.Request) {
		gt.NoError(t, r.ParseForm())
		gt.Equal(t, r.PostForm.Get("client_id"), "test-client")
		gt.Equal(t, r.PostForm.Get("scope"), "repo")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"device_code":"dev-123","user_code":"ABCD-1234","verification_uri":"https://github.com/login/device","expires_in":900,"interval":1}`))
	})
	mux.HandleFunc("/login/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		gt.NoError(t, r.ParseForm())
		gt.Equal(t, r.PostForm.Get("device_code"), "dev-123")
		i := int(polls.Add(1)) - 1
		if i >= len(tokenResponses) {
			i = len(tokenResponses) - 1
		}
		w.Header().Set("Content-Type", "application/json")
		// GitHub answers pending and failure states with 200.
		_, _ = w.Write([]byte(tokenResponses[i]))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return github.NewDeviceAuthenticator("test-client", github.WithEndpoint(oauth2.Endpoint{
		DeviceAuthURL: srv.URL + "/login/device/code",
		TokenURL:      srv.URL + "/login/oauth/access_token",
	})), &polls
}

func startAndWait(t *testing.T, auth *github.DeviceAuthenticator) (model.GitHubToken, error) {
	t.Helper()
	ctx := context.Background()
	code, err := auth.StartDeviceAuth(ctx)
	gt.NoError(t, err).Required()
	gt.Equal(t, code.UserCode, "ABCD-1234")
	gt.Equal(t, code.VerificationURI, "https://github.com/login/device")
	gt.Equal(t, code.DeviceCode, "dev-123")
	return auth.WaitDeviceToken(ctx, code)
}

func TestDeviceFlow(t *testing.T) {
	t.Run("token after pending", func(t *testing.T) {
		auth, polls := newDeviceServer(t,
			`{"error":"authorization_pending"}`,
			`{"error":"authorization_pending"}`,
			`{"access_token":"gho_abc","token_type":"bearer","scope":"repo"}`)
		token, err := startAndWait(t, auth)
		gt.NoError(t, err).Required()
		gt.Equal(t, token, model.GitHubToken("gho_abc"))
		gt.Equal(t, polls.Load(), int32(3))
	})

	t.Run("expired", func(t *testing.T) {
		auth, _ := newDeviceServer(t, `{"error":"expired_token"}`)
		_, err := startAndWait(t, auth)
		gt.Error(t, err).Is(interfaces.ErrDeviceCodeExpired)
	})

	t.Run("denied", func(t *testing.T) {
		auth, _ := newDeviceServer(t, `{"error":"access_denied"}`)
		_, err := startAndWait(t, auth)
		gt.Error(t, err).Is(interfaces.ErrAccessDenied)
	})

	t.Run("device flow disabled", func(t *testing.T) {
		auth, _ := newDeviceServer(t, `{"error":"device_flow_disabled"}`)
		_, err := startAndWait(t, auth)
		gt.Error(t, err).Is(interfaces.ErrDeviceFlowDisabled)
	})
}

func TestStartDeviceAuthDisabled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"device_flow_disabled"}`))
	}))
	t.Cleanup(srv.Close)
	auth := github.NewDeviceAuthenticator("test-client", github.WithEndpoint(oauth2.Endpoint{
		DeviceAuthURL: srv.URL, TokenURL: srv.URL,
	}))
	_, err := auth.StartDeviceAuth(context.Background())
	gt.Error(t, err).Is(interfaces.ErrDeviceFlowDisabled)
}
