// Package portainer reads stacks and environments from a Portainer instance's HTTP API.
package portainer

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"emperror.dev/errors"

	"github.com/getarcaneapp/arcane/backend/v2/internal/common"
	"github.com/getarcaneapp/arcane/backend/v2/pkg/utils/httpx"
)

// Portainer's numeric stack types.
const (
	StackTypeSwarm      = 1
	StackTypeCompose    = 2
	StackTypeKubernetes = 3
)

// Portainer's numeric stack states.
const (
	StackStatusActive   = 1
	StackStatusInactive = 2
)

const (
	requestTimeoutInternal   = 30 * time.Second
	maxRedirectsInternal     = 5
	maxResponseBytesInternal = 8 << 20
	apiKeyHeaderInternal     = "X-API-Key" // #nosec G101: header name, not a credential
)

// Pair is a Portainer name/value pair, used for stack environment variables.
type Pair struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Stack is a stack as reported by Portainer.
type Stack struct {
	ID         int    `json:"Id"`
	Name       string `json:"Name"`
	Type       int    `json:"Type"`
	EndpointID int    `json:"EndpointId"`
	EntryPoint string `json:"EntryPoint"`
	Status     int    `json:"Status"`
	CreatedBy  string `json:"CreatedBy"`
	UpdatedBy  string `json:"UpdatedBy"`
	Env        []Pair `json:"Env"`
}

// Endpoint is a Portainer environment a stack is deployed to.
type Endpoint struct {
	ID   int    `json:"Id"`
	Name string `json:"Name"`
}

// Config describes how to reach and authenticate against a Portainer instance.
type Config struct {
	URL           string
	APIKey        string
	Username      string
	Password      string
	SkipTLSVerify bool
	// BaseClient supplies proxy and transport settings; its timeout is replaced.
	BaseClient *http.Client
}

// Client calls the Portainer API on behalf of one import request.
type Client struct {
	httpClient *http.Client
	baseURL    string
	apiKey     string
	username   string
	password   string
	token      string
}

// New validates connection settings and builds a client for them.
func New(cfg Config) (*Client, error) {
	parsed, err := httpx.ValidateOutboundHTTPURL(cfg.URL)
	if err != nil {
		return nil, common.Classify(common.ErrBadRequest, errors.WrapIf(err, "invalid Portainer URL"))
	}

	apiKey := strings.TrimSpace(cfg.APIKey)
	username := strings.TrimSpace(cfg.Username)
	if apiKey == "" && (username == "" || cfg.Password == "") {
		return nil, common.Classify(common.ErrBadRequest, errors.New("a Portainer access token, or a username and password, is required"))
	}

	httpClient, err := newHTTPClientInternal(cfg.BaseClient, cfg.SkipTLSVerify)
	if err != nil {
		return nil, err
	}

	return &Client{
		httpClient: httpClient,
		baseURL:    normalizeBaseURLInternal(parsed),
		apiKey:     apiKey,
		username:   username,
		password:   cfg.Password,
	}, nil
}

// Version returns the Portainer version reported by the instance.
func (c *Client) Version(ctx context.Context) (string, error) {
	var status struct {
		Version string `json:"Version"`
	}
	if err := c.getInternal(ctx, "/api/status", &status); err != nil {
		return "", err
	}
	return status.Version, nil
}

// Stacks returns every stack visible to the authenticated Portainer user.
func (c *Client) Stacks(ctx context.Context) ([]Stack, error) {
	var stacks []Stack
	if err := c.getInternal(ctx, "/api/stacks", &stacks); err != nil {
		return nil, err
	}
	return stacks, nil
}

// Endpoints returns the Portainer environments stacks can be deployed to.
func (c *Client) Endpoints(ctx context.Context) ([]Endpoint, error) {
	var endpoints []Endpoint
	if err := c.getInternal(ctx, "/api/endpoints", &endpoints); err != nil {
		return nil, err
	}
	return endpoints, nil
}

// StackFile returns the Compose file content of a stack.
func (c *Client) StackFile(ctx context.Context, stackID int) (string, error) {
	var file struct {
		StackFileContent string `json:"StackFileContent"`
	}
	if err := c.getInternal(ctx, "/api/stacks/"+strconv.Itoa(stackID)+"/file", &file); err != nil {
		return "", err
	}
	if strings.TrimSpace(file.StackFileContent) == "" {
		return "", common.Classify(common.ErrNotFound, errors.Errorf("Portainer stack %d has no Compose file content", stackID))
	}
	return file.StackFileContent, nil
}

func (c *Client) getInternal(ctx context.Context, path string, out any) error {
	if err := c.ensureAuthenticatedInternal(ctx); err != nil {
		return err
	}

	body, err := c.doInternal(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return common.Classify(common.ErrUnavailable, errors.WrapIff(err, "unexpected response from Portainer at %s", path))
	}
	return nil
}

// ensureAuthenticatedInternal exchanges username and password for a session
// token. Access tokens authenticate every request on their own.
func (c *Client) ensureAuthenticatedInternal(ctx context.Context) error {
	if c.apiKey != "" || c.token != "" {
		return nil
	}

	payload, err := json.Marshal(map[string]string{"Username": c.username, "Password": c.password})
	if err != nil {
		return errors.WrapIf(err, "failed to encode Portainer credentials")
	}

	body, err := c.doInternal(ctx, http.MethodPost, "/api/auth", payload)
	if err != nil {
		return err
	}

	var auth struct {
		JWT string `json:"jwt"`
	}
	if err := json.Unmarshal(body, &auth); err != nil {
		return common.Classify(common.ErrUnavailable, errors.WrapIf(err, "unexpected response from Portainer sign-in"))
	}
	if strings.TrimSpace(auth.JWT) == "" {
		return common.Classify(common.ErrBadRequest, errors.New("Portainer sign-in did not return a session token"))
	}

	c.token = auth.JWT
	return nil
}

func (c *Client) doInternal(ctx context.Context, method, path string, payload []byte) ([]byte, error) {
	var requestBody io.Reader
	if len(payload) > 0 {
		requestBody = bytes.NewReader(payload)
	}

	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, requestBody)
	if err != nil {
		return nil, common.Classify(common.ErrBadRequest, errors.WrapIff(err, "failed to build Portainer request for %s", path))
	}
	request.Header.Set("Accept", "application/json")
	if len(payload) > 0 {
		request.Header.Set("Content-Type", "application/json")
	}
	switch {
	case c.apiKey != "":
		request.Header.Set(apiKeyHeaderInternal, c.apiKey)
	case c.token != "":
		request.Header.Set("Authorization", "Bearer "+c.token)
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, common.Classify(common.ErrUnavailable, errors.WrapIf(err, "failed to reach Portainer"))
	}
	defer func() { _ = response.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytesInternal+1))
	if err != nil {
		return nil, common.Classify(common.ErrUnavailable, errors.WrapIf(err, "failed to read the Portainer response"))
	}
	if len(body) > maxResponseBytesInternal {
		return nil, common.Classify(common.ErrUnavailable, errors.Errorf("Portainer response for %s exceeds %d bytes", path, maxResponseBytesInternal))
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, statusErrorInternal(response.StatusCode, path, body)
	}

	return body, nil
}

// statusErrorInternal classifies Portainer's failure responses. Authentication
// and authorization failures are the caller's input, so they stay bad requests
// rather than becoming Arcane's own 401/403.
func statusErrorInternal(statusCode int, path string, body []byte) error {
	detail := portainerMessageInternal(body)

	switch statusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		message := "Portainer rejected the supplied credentials"
		if detail != "" {
			message += ": " + detail
		}
		return common.Classify(common.ErrBadRequest, errors.New(message))
	case http.StatusNotFound:
		message := "Portainer resource not found"
		if detail != "" {
			message += ": " + detail
		}
		return common.Classify(common.ErrNotFound, errors.New(message))
	}

	message := "Portainer request " + path + " failed with status " + strconv.Itoa(statusCode)
	if detail != "" {
		message += ": " + detail
	}
	return common.Classify(common.ErrUnavailable, errors.New(message))
}

// portainerMessageInternal extracts Portainer's error message from a response body.
func portainerMessageInternal(body []byte) string {
	var payload struct {
		Message string `json:"message"`
		Details string `json:"details"`
	}
	if err := json.Unmarshal(body, &payload); err == nil {
		if message := strings.TrimSpace(payload.Message); message != "" {
			return message
		}
		if details := strings.TrimSpace(payload.Details); details != "" {
			return details
		}
	}

	text := strings.TrimSpace(string(body))
	if len(text) > 200 {
		return text[:200]
	}
	return text
}

// normalizeBaseURLInternal drops query, fragment, and a trailing "/api" so
// callers can paste any Portainer URL from their browser.
func normalizeBaseURLInternal(parsed *url.URL) string {
	base := &url.URL{Scheme: strings.ToLower(parsed.Scheme), Host: parsed.Host, Path: parsed.Path}
	base.Path = strings.TrimSuffix(base.Path, "/")
	base.Path = strings.TrimSuffix(base.Path, "/api")
	base.Path = strings.TrimSuffix(base.Path, "/")
	return base.String()
}

func newHTTPClientInternal(base *http.Client, skipTLSVerify bool) (*http.Client, error) {
	client := &http.Client{}
	if base != nil {
		*client = *base
	}
	client.Timeout = requestTimeoutInternal
	client.CheckRedirect = refuseCrossHostRedirectInternal

	if !skipTLSVerify {
		return client, nil
	}

	insecure, err := httpx.NewInsecureTLSClient(client)
	if err != nil {
		return nil, common.Classify(common.ErrBadRequest, errors.WrapIf(err, "failed to configure the Portainer client"))
	}
	return insecure, nil
}

// refuseCrossHostRedirectInternal stops the access token or session token from
// being replayed to a host the operator did not configure. Go only strips
// sensitive headers on cross-domain redirects, and X-API-Key is not one of them.
func refuseCrossHostRedirectInternal(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirectsInternal {
		return errors.New("Portainer redirected too many times")
	}
	if len(via) > 0 && req.URL.Host != via[0].URL.Host {
		return errors.Errorf("Portainer redirected to another host (%s)", req.URL.Host)
	}
	return nil
}
