package config

import (
	"github.com/m-mizutani/goerr/v2"
	"github.com/urfave/cli/v3"

	"github.com/m-mizutani/octbox/pkg/adapter/github"
)

// defaultClientID is the Client ID of the octbox OAuth app. The device flow
// uses no client secret, so the ID is not a secret.
const defaultClientID = "Ov23lifmj6pSAE32qsGZ"

type GitHub struct {
	clientID string
}

func (x *GitHub) Flags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:        "client-id",
			Category:    "GitHub",
			Usage:       "Client ID of the GitHub OAuth app used for the device flow",
			Value:       defaultClientID,
			Sources:     cli.EnvVars("OCTBOX_CLIENT_ID"),
			Destination: &x.clientID,
		},
	}
}

func (x *GitHub) NewDeviceAuthenticator() (*github.DeviceAuthenticator, error) {
	if x.clientID == "" {
		return nil, goerr.New("--client-id is empty")
	}
	return github.NewDeviceAuthenticator(x.clientID), nil
}
