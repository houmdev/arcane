package httpx

import (
	"crypto/tls"
	"net/http"
	"time"

	httpxtypes "github.com/getarcaneapp/arcane/types/v2/httpx"
)

// NewHTTPClient builds a client with its own transport and the supplied timeouts.
func NewHTTPClient(options httpxtypes.ClientOptions) *http.Client {
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   options.TLSHandshakeTimeout,
		ExpectContinueTimeout: 1 * time.Second,
		ForceAttemptHTTP2:     true,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   options.Timeout,
	}
}

// NewInsecureTLSClient returns a copy of base whose transport skips TLS
// certificate verification. Used for user-configured endpoints, such as OIDC
// issuers or Portainer instances, that present self-signed certificates.
func NewInsecureTLSClient(base *http.Client) (*http.Client, error) {
	client := &http.Client{}
	if base != nil {
		*client = *base
	}

	transport, err := cloneHTTPTransportInternal(client.Transport)
	if err != nil {
		return nil, err
	}

	if transport.TLSClientConfig == nil {
		// #nosec G402 - the caller explicitly opted out of TLS verification for this endpoint
		transport.TLSClientConfig = &tls.Config{
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: true,
		}
	} else {
		transport.TLSClientConfig = transport.TLSClientConfig.Clone()
		transport.TLSClientConfig.InsecureSkipVerify = true
	}

	// Enable HTTP/2 even with a custom TLS configuration.
	if transport.Protocols == nil {
		transport.Protocols = new(http.Protocols)
		transport.Protocols.SetHTTP1(true)
	}
	transport.Protocols.SetHTTP2(true)

	client.Transport = transport
	return client, nil
}
