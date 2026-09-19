package portainer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/getarcaneapp/arcane/backend/v2/internal/common"
)

func TestNewRequiresCredentials(t *testing.T) {
	_, err := New(Config{URL: "https://portainer.example.com"})

	require.ErrorIs(t, err, common.ErrBadRequest)
	require.ErrorContains(t, err, "access token")
}

func TestNewRejectsUnsupportedURL(t *testing.T) {
	_, err := New(Config{URL: "ftp://portainer.example.com", APIKey: "token"})

	require.ErrorIs(t, err, common.ErrBadRequest)
}

func TestClientSendsAccessTokenAndTrimsAPISuffix(t *testing.T) {
	var requestedPaths []string
	var apiKeys []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPaths = append(requestedPaths, r.URL.Path)
		apiKeys = append(apiKeys, r.Header.Get("X-API-Key"))
		switch r.URL.Path {
		case "/api/stacks":
			_, _ = w.Write([]byte(`[{"Id":7,"Name":"blog","Type":2,"EndpointId":1,"Env":[{"name":"TAG","value":"latest"}]}]`))
		case "/api/stacks/7/file":
			_, _ = w.Write([]byte(`{"StackFileContent":"services:\n  app:\n    image: nginx\n"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client, err := New(Config{URL: server.URL + "/api/", APIKey: "portainer-token"})
	require.NoError(t, err)

	stacks, err := client.Stacks(context.Background())
	require.NoError(t, err)
	require.Len(t, stacks, 1)
	require.Equal(t, "blog", stacks[0].Name)
	require.Equal(t, StackTypeCompose, stacks[0].Type)
	require.Equal(t, []Pair{{Name: "TAG", Value: "latest"}}, stacks[0].Env)

	content, err := client.StackFile(context.Background(), 7)
	require.NoError(t, err)
	require.Equal(t, "services:\n  app:\n    image: nginx\n", content)

	require.Equal(t, []string{"/api/stacks", "/api/stacks/7/file"}, requestedPaths)
	require.Equal(t, []string{"portainer-token", "portainer-token"}, apiKeys)
}

func TestClientSignsInWithUsernameAndPassword(t *testing.T) {
	var authorizations []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth":
			_, _ = w.Write([]byte(`{"jwt":"session-token"}`))
		case "/api/stacks":
			authorizations = append(authorizations, r.Header.Get("Authorization"))
			_, _ = w.Write([]byte(`[]`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client, err := New(Config{URL: server.URL, Username: "admin", Password: "secret"})
	require.NoError(t, err)

	_, err = client.Stacks(context.Background())
	require.NoError(t, err)
	_, err = client.Stacks(context.Background())
	require.NoError(t, err)

	require.Equal(t, []string{"Bearer session-token", "Bearer session-token"}, authorizations)
}

func TestClientReportsRejectedCredentialsAsBadRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"Invalid API key"}`))
	}))
	defer server.Close()

	client, err := New(Config{URL: server.URL, APIKey: "wrong"})
	require.NoError(t, err)

	_, err = client.Stacks(context.Background())
	require.ErrorIs(t, err, common.ErrBadRequest)
	require.ErrorContains(t, err, "Invalid API key")
}

func TestClientReportsServerFailureAsUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"boom"}`))
	}))
	defer server.Close()

	client, err := New(Config{URL: server.URL, APIKey: "token"})
	require.NoError(t, err)

	_, err = client.Stacks(context.Background())
	require.ErrorIs(t, err, common.ErrUnavailable)
	require.ErrorContains(t, err, "boom")
}

func TestClientRejectsEmptyStackFile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"StackFileContent":"   "}`))
	}))
	defer server.Close()

	client, err := New(Config{URL: server.URL, APIKey: "token"})
	require.NoError(t, err)

	_, err = client.StackFile(context.Background(), 3)
	require.ErrorIs(t, err, common.ErrNotFound)
}

func TestClientReadsVersion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/status", r.URL.Path)
		_, _ = w.Write([]byte(`{"Version":"2.21.0"}`))
	}))
	defer server.Close()

	client, err := New(Config{URL: server.URL, APIKey: "token"})
	require.NoError(t, err)

	version, err := client.Version(context.Background())
	require.NoError(t, err)
	require.Equal(t, "2.21.0", version)
}

func TestClientRefusesCrossHostRedirect(t *testing.T) {
	var forwardedKeys []string
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwardedKeys = append(forwardedKeys, r.Header.Get("X-API-Key"))
		_, _ = w.Write([]byte(`[]`))
	}))
	defer elsewhere.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/api/stacks", http.StatusFound)
	}))
	defer server.Close()

	client, err := New(Config{URL: server.URL, APIKey: "token"})
	require.NoError(t, err)

	_, err = client.Stacks(context.Background())
	require.ErrorIs(t, err, common.ErrUnavailable)
	require.ErrorContains(t, err, "redirected to another host")
	require.Empty(t, forwardedKeys)
}
