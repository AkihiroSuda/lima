package client

// Forked from https://github.com/rootless-containers/rootlesskit/blob/v0.14.2/pkg/api/client/client.go
// Apache License 2.0

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/lima-vm/lima/pkg/hostagent/api"
	"github.com/lima-vm/lima/pkg/httpclientutil"
)

type HostAgentClient interface {
	HTTPClient() *http.Client
	Info(context.Context) (*api.Info, error)
}

// NewHostAgentClient creates a client.
// socketPath is a path to the UNIX socket, without unix:// prefix.
func NewHostAgentClient(socketPath string) (HostAgentClient, error) {
	port, err := strconv.Atoi(socketPath)
	if err != nil {
		hc, err := httpclientutil.NewHTTPClientWithSocketPath(socketPath)
		if err != nil {
			return nil, err
		}
		return NewHostAgentClientWithHTTPClient(hc, "lima-hostagent"), nil
	} else {
		hc, err := httpclientutil.NewHTTPClient()
		if err != nil {
			return nil, err
		}
		address := fmt.Sprintf("127.0.0.1:%d", port)
		return NewHostAgentClientWithHTTPClient(hc, address), nil
	}
}

func NewHostAgentClientWithHTTPClient(hc *http.Client, address string) HostAgentClient {
	return &client{
		Client:  hc,
		version: "v1",
		address: address,
	}
}

type client struct {
	*http.Client
	// version is always "v1"
	// TODO(AkihiroSuda): negotiate the version
	version string
	address string
}

func (c *client) HTTPClient() *http.Client {
	return c.Client
}

func (c *client) Info(ctx context.Context) (*api.Info, error) {
	u := fmt.Sprintf("http://%s/%s/info", c.address, c.version)
	resp, err := httpclientutil.Get(ctx, c.HTTPClient(), u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var info api.Info
	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(&info); err != nil {
		return nil, err
	}
	return &info, nil
}
