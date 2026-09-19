package project

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/getarcaneapp/arcane/backend/v2/internal/common"
	"github.com/getarcaneapp/arcane/backend/v2/internal/config"
	"github.com/getarcaneapp/arcane/backend/v2/internal/event"
	"github.com/getarcaneapp/arcane/backend/v2/pkg/portainer"
	"github.com/getarcaneapp/arcane/backend/v2/pkg/projects"
	projecttypes "github.com/getarcaneapp/arcane/types/v2/project"
)

const portainerTestStacksInternal = `[
	{"Id":1,"Name":"blog","Type":2,"EndpointId":1,"Status":1,"Env":[{"name":"TAG","value":"1.2.3"},{"name":"GREETING","value":"hello world"}]},
	{"Id":2,"Name":"swarm-api","Type":1,"EndpointId":2,"Status":2,"Env":[]},
	{"Id":3,"Name":"cluster-app","Type":3,"EndpointId":3,"Status":1,"Env":[]}
]`

func newPortainerTestServerInternal(t *testing.T) *httptest.Server {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/status":
			_, _ = w.Write([]byte(`{"Version":"2.21.0"}`))
		case "/api/endpoints":
			_, _ = w.Write([]byte(`[{"Id":1,"Name":"local"},{"Id":2,"Name":"swarm-cluster"}]`))
		case "/api/stacks":
			_, _ = w.Write([]byte(portainerTestStacksInternal))
		case "/api/stacks/1/file":
			_, _ = w.Write([]byte(`{"StackFileContent":"services:\n  blog:\n    image: nginx:${TAG}\n"}`))
		case "/api/stacks/2/file":
			_, _ = w.Write([]byte(`{"StackFileContent":"services:\n  api:\n    image: traefik:latest\n"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	return server
}

func newPortainerImportTestServiceInternal(t *testing.T) (*ProjectService, string) {
	t.Helper()

	ctx := context.Background()
	db := setupProjectTestDB(t)
	projectsDir := t.TempDir()
	t.Setenv("PROJECTS_DIRECTORY", projectsDir)

	settingsService, err := newSettingsServiceForTestInternal(t, ctx, db)
	require.NoError(t, err)
	require.NoError(t, settingsService.SetStringSetting(ctx, "projectsDirectory", projectsDir))

	service := NewProjectService(db, settingsService, event.NewEventService(db, nil, nil), nil, nil, nil, nil, nil, config.Load()).
		WithHTTPClient(http.DefaultClient)

	return service, projectsDir
}

func TestProjectService_ListPortainerStacks_ReportsImportableStacks(t *testing.T) {
	ctx := context.Background()
	service, _ := newPortainerImportTestServiceInternal(t)
	server := newPortainerTestServerInternal(t)

	require.NoError(t, service.db.WithContext(ctx).Create(&Project{ID: "existing-blog", Name: "blog", Path: t.TempDir()}).Error)

	list, err := service.ListPortainerStacks(ctx, projecttypes.PortainerConnection{URL: server.URL, AccessToken: "token"})
	require.NoError(t, err)
	require.Equal(t, "2.21.0", list.PortainerVersion)
	require.Len(t, list.Stacks, 3)

	byName := map[string]projecttypes.PortainerStack{}
	for _, stack := range list.Stacks {
		byName[stack.Name] = stack
	}

	blog := byName["blog"]
	require.Equal(t, projecttypes.PortainerStackKindCompose, blog.Kind)
	require.Equal(t, projecttypes.PortainerStackStateActive, blog.State)
	require.Equal(t, "local", blog.EndpointName)
	require.Equal(t, 2, blog.EnvCount)
	require.True(t, blog.Importable)
	require.Equal(t, "existing-blog", blog.ExistingProjectID)

	swarm := byName["swarm-api"]
	require.Equal(t, projecttypes.PortainerStackKindSwarm, swarm.Kind)
	require.Equal(t, projecttypes.PortainerStackStateInactive, swarm.State)
	require.Equal(t, "swarm-cluster", swarm.EndpointName)
	require.True(t, swarm.Importable)
	require.Empty(t, swarm.ExistingProjectID)

	kubernetes := byName["cluster-app"]
	require.Equal(t, projecttypes.PortainerStackKindKubernetes, kubernetes.Kind)
	require.False(t, kubernetes.Importable)
	require.Contains(t, kubernetes.SkipReason, "Kubernetes")
}

func TestProjectService_ImportPortainerStacks_CreatesProjectsWithStackEnv(t *testing.T) {
	ctx := context.Background()
	service, projectsDir := newPortainerImportTestServiceInternal(t)
	server := newPortainerTestServerInternal(t)

	result, err := service.ImportPortainerStacks(ctx, projecttypes.PortainerImport{
		Connection: projecttypes.PortainerConnection{URL: server.URL, AccessToken: "token"},
		StackIDs:   []int{1, 2},
	}, common.User{ID: "u1", Username: "tester"})
	require.NoError(t, err)
	require.Equal(t, 2, result.Imported)
	require.Zero(t, result.Failed)
	require.Len(t, result.Stacks, 2)

	require.True(t, result.Stacks[0].Imported)
	require.Equal(t, "blog", result.Stacks[0].ProjectName)
	require.NotEmpty(t, result.Stacks[0].ProjectID)

	composePath := filepath.Join(projectsDir, "blog", projects.DefaultComposeFileName)
	compose, err := os.ReadFile(composePath) //nolint:gosec // test-owned temp path
	require.NoError(t, err)
	require.Contains(t, string(compose), "image: nginx:${TAG}")

	envContent, err := os.ReadFile(filepath.Join(projectsDir, "blog", ".env")) //nolint:gosec // test-owned temp path
	require.NoError(t, err)
	require.Equal(t, "TAG=1.2.3\nGREETING=\"hello world\"\n", string(envContent))

	require.True(t, result.Stacks[1].Imported)
	require.Equal(t, "swarm-api", result.Stacks[1].ProjectName)
	swarmEnv, err := os.ReadFile(filepath.Join(projectsDir, "swarm-api", ".env")) //nolint:gosec // test-owned temp path
	require.NoError(t, err)
	require.Empty(t, string(swarmEnv))

	var stored []Project
	require.NoError(t, service.db.WithContext(ctx).Order("name").Find(&stored).Error)
	require.Len(t, stored, 2)
	require.Equal(t, "blog", stored[0].Name)
	require.Equal(t, "swarm-api", stored[1].Name)
}

func TestProjectService_ImportPortainerStacks_ReportsPerStackFailures(t *testing.T) {
	ctx := context.Background()
	service, projectsDir := newPortainerImportTestServiceInternal(t)
	server := newPortainerTestServerInternal(t)

	result, err := service.ImportPortainerStacks(ctx, projecttypes.PortainerImport{
		Connection: projecttypes.PortainerConnection{URL: server.URL, AccessToken: "token"},
		StackIDs:   []int{3, 99, 1},
	}, common.User{ID: "u1", Username: "tester"})
	require.NoError(t, err)
	require.Equal(t, 1, result.Imported)
	require.Equal(t, 2, result.Failed)

	require.False(t, result.Stacks[0].Imported)
	require.Contains(t, result.Stacks[0].Error, "Kubernetes")
	require.False(t, result.Stacks[1].Imported)
	require.Contains(t, result.Stacks[1].Error, "not found")
	require.True(t, result.Stacks[2].Imported)
	require.DirExists(t, filepath.Join(projectsDir, "blog"))
}

func TestProjectService_ImportPortainerStacks_ImportsRepeatedStackOnce(t *testing.T) {
	ctx := context.Background()
	service, projectsDir := newPortainerImportTestServiceInternal(t)
	server := newPortainerTestServerInternal(t)

	result, err := service.ImportPortainerStacks(ctx, projecttypes.PortainerImport{
		Connection: projecttypes.PortainerConnection{URL: server.URL, AccessToken: "token"},
		StackIDs:   []int{1, 1},
	}, common.User{ID: "u1", Username: "tester"})
	require.NoError(t, err)
	require.Equal(t, 1, result.Imported)
	require.Len(t, result.Stacks, 1)
	require.NoDirExists(t, filepath.Join(projectsDir, "blog-2"))
}

func TestProjectService_ImportPortainerStacks_RequiresStackSelection(t *testing.T) {
	service, _ := newPortainerImportTestServiceInternal(t)

	_, err := service.ImportPortainerStacks(context.Background(), projecttypes.PortainerImport{
		Connection: projecttypes.PortainerConnection{URL: "https://portainer.example.com", AccessToken: "token"},
	}, common.User{ID: "u1", Username: "tester"})

	require.ErrorIs(t, err, common.ErrBadRequest)
}

func TestPortainerEnvContentEscapesValuesAndSkipsUnnamedPairs(t *testing.T) {
	content := portainerEnvContentInternal([]portainer.Pair{
		{Name: "PRICE", Value: "$5 a month"},
		{Name: "EMPTY", Value: ""},
		{Name: "  ", Value: "dropped"},
		{Name: "MULTI", Value: "line one\nline two"},
	})

	require.NotNil(t, content)
	require.Equal(t, "PRICE=\"$$5 a month\"\nEMPTY=\nMULTI=\"line one\\nline two\"\n", *content)
	require.Nil(t, portainerEnvContentInternal(nil))
}
