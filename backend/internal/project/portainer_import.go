package project

import (
	"context"
	"log/slog"
	"sort"
	"strings"

	"emperror.dev/errors"

	"github.com/getarcaneapp/arcane/backend/v2/internal/common"
	"github.com/getarcaneapp/arcane/backend/v2/pkg/portainer"
	"github.com/getarcaneapp/arcane/backend/v2/pkg/projects"
	projecttypes "github.com/getarcaneapp/arcane/types/v2/project"
)

// maxPortainerImportStacksInternal caps one import request; each stack costs a
// Portainer round trip plus a project create.
const maxPortainerImportStacksInternal = 100

// ListPortainerStacks discovers the stacks on a Portainer instance and reports
// which ones Arcane can import as projects.
func (s *ProjectService) ListPortainerStacks(ctx context.Context, connection projecttypes.PortainerConnection) (*projecttypes.PortainerStackList, error) {
	client, err := s.portainerClientInternal(connection)
	if err != nil {
		return nil, err
	}

	stacks, err := client.Stacks(ctx)
	if err != nil {
		return nil, err
	}

	endpointNames := s.portainerEndpointNamesInternal(ctx, client)
	existingProjects, err := s.projectIDsByNameInternal(ctx)
	if err != nil {
		return nil, err
	}

	summaries := make([]projecttypes.PortainerStack, 0, len(stacks))
	for _, stack := range stacks {
		summaries = append(summaries, portainerStackSummaryInternal(stack, endpointNames, existingProjects))
	}
	sort.Slice(summaries, func(i, j int) bool {
		if summaries[i].Name == summaries[j].Name {
			return summaries[i].ID < summaries[j].ID
		}
		return summaries[i].Name < summaries[j].Name
	})

	version, err := client.Version(ctx)
	if err != nil {
		slog.DebugContext(ctx, "failed to read the Portainer version", "error", err)
	}

	return &projecttypes.PortainerStackList{Stacks: summaries, PortainerVersion: version}, nil
}

// ImportPortainerStacks creates a project from each selected Portainer stack,
// carrying over its Compose file and stack environment variables. Stacks are
// imported independently: one failure does not abort the rest.
func (s *ProjectService) ImportPortainerStacks(ctx context.Context, request projecttypes.PortainerImport, user common.User) (*projecttypes.PortainerImportResult, error) {
	if len(request.StackIDs) == 0 {
		return nil, common.Classify(common.ErrBadRequest, errors.New("select at least one Portainer stack to import"))
	}
	if len(request.StackIDs) > maxPortainerImportStacksInternal {
		return nil, common.Classify(common.ErrBadRequest, errors.Errorf("at most %d Portainer stacks can be imported at once", maxPortainerImportStacksInternal))
	}

	client, err := s.portainerClientInternal(request.Connection)
	if err != nil {
		return nil, err
	}

	stacks, err := client.Stacks(ctx)
	if err != nil {
		return nil, err
	}
	stacksByID := make(map[int]portainer.Stack, len(stacks))
	for _, stack := range stacks {
		stacksByID[stack.ID] = stack
	}

	result := &projecttypes.PortainerImportResult{Stacks: make([]projecttypes.PortainerImportedStack, 0, len(request.StackIDs))}
	// A repeated stack ID would otherwise import the same stack twice, with the
	// second copy silently renamed to "<name>-2".
	requested := make(map[int]struct{}, len(request.StackIDs))
	for _, stackID := range request.StackIDs {
		if _, seen := requested[stackID]; seen {
			continue
		}
		requested[stackID] = struct{}{}

		outcome := s.importPortainerStackInternal(ctx, client, stacksByID, stackID, user)
		if outcome.Imported {
			result.Imported++
		} else {
			result.Failed++
		}
		result.Stacks = append(result.Stacks, outcome)
	}

	return result, nil
}

func (s *ProjectService) importPortainerStackInternal(ctx context.Context, client *portainer.Client, stacksByID map[int]portainer.Stack, stackID int, user common.User) projecttypes.PortainerImportedStack {
	outcome := projecttypes.PortainerImportedStack{StackID: stackID}

	stack, found := stacksByID[stackID]
	if !found {
		outcome.Error = "Stack was not found on the Portainer instance"
		return outcome
	}
	outcome.StackName = stack.Name

	if reason := portainerStackSkipReasonInternal(stack); reason != "" {
		outcome.Error = reason
		return outcome
	}

	composeContent, err := client.StackFile(ctx, stack.ID)
	if err != nil {
		outcome.Error = err.Error()
		return outcome
	}

	created, err := s.CreateProject(ctx, stack.Name, composeContent, portainerEnvContentInternal(stack.Env), projecttypes.CreateProjectWorkspaceManifest{}, nil, nil, nil, user)
	if err != nil {
		slog.WarnContext(ctx, "failed to import Portainer stack", "stackId", stack.ID, "stackName", stack.Name, "error", err)
		outcome.Error = err.Error()
		return outcome
	}

	outcome.Imported = true
	outcome.ProjectID = created.ID
	outcome.ProjectName = created.Name
	return outcome
}

func (s *ProjectService) portainerClientInternal(connection projecttypes.PortainerConnection) (*portainer.Client, error) {
	return portainer.New(portainer.Config{
		URL:           connection.URL,
		APIKey:        connection.AccessToken,
		Username:      connection.Username,
		Password:      connection.Password,
		SkipTLSVerify: connection.SkipTLSVerify,
		BaseClient:    s.httpClient,
	})
}

// portainerEndpointNamesInternal maps Portainer environment IDs to their names.
// Restricted access tokens cannot list environments, so the names stay optional.
func (s *ProjectService) portainerEndpointNamesInternal(ctx context.Context, client *portainer.Client) map[int]string {
	endpoints, err := client.Endpoints(ctx)
	if err != nil {
		slog.DebugContext(ctx, "failed to list Portainer environments", "error", err)
		return nil
	}

	names := make(map[int]string, len(endpoints))
	for _, endpoint := range endpoints {
		names[endpoint.ID] = endpoint.Name
	}
	return names
}

// projectIDsByNameInternal indexes existing projects by normalized name so
// discovery can flag stacks that would import as a second copy.
func (s *ProjectService) projectIDsByNameInternal(ctx context.Context) (map[string]string, error) {
	var rows []Project
	if err := s.db.WithContext(ctx).Model(&Project{}).Select("id", "name").Find(&rows).Error; err != nil {
		return nil, errors.WrapIf(err, "failed to list existing projects")
	}

	byName := make(map[string]string, len(rows))
	for _, row := range rows {
		byName[projects.NormalizeProjectName(row.Name)] = row.ID
	}
	return byName, nil
}

func portainerStackSummaryInternal(stack portainer.Stack, endpointNames map[int]string, existingProjects map[string]string) projecttypes.PortainerStack {
	summary := projecttypes.PortainerStack{
		ID:           stack.ID,
		Name:         stack.Name,
		Kind:         portainerStackKindInternal(stack.Type),
		State:        portainerStackStateInternal(stack.Status),
		EndpointName: endpointNames[stack.EndpointID],
		EnvCount:     len(stack.Env),
	}

	summary.SkipReason = portainerStackSkipReasonInternal(stack)
	summary.Importable = summary.SkipReason == ""
	summary.ExistingProjectID = existingProjects[projects.NormalizeProjectName(stack.Name)]

	return summary
}

// portainerStackSkipReasonInternal explains why a stack cannot become a
// project, and is empty for importable stacks.
func portainerStackSkipReasonInternal(stack portainer.Stack) string {
	if strings.TrimSpace(stack.Name) == "" {
		return "Stack has no name"
	}

	switch portainerStackKindInternal(stack.Type) {
	case projecttypes.PortainerStackKindCompose, projecttypes.PortainerStackKindSwarm:
		return ""
	case projecttypes.PortainerStackKindKubernetes:
		return "Kubernetes stacks are manifests rather than Compose projects"
	default:
		return "Stack uses a type Arcane cannot read as a Compose project"
	}
}

func portainerStackKindInternal(stackType int) string {
	switch stackType {
	case portainer.StackTypeSwarm:
		return projecttypes.PortainerStackKindSwarm
	case portainer.StackTypeCompose:
		return projecttypes.PortainerStackKindCompose
	case portainer.StackTypeKubernetes:
		return projecttypes.PortainerStackKindKubernetes
	default:
		return projecttypes.PortainerStackKindUnknown
	}
}

func portainerStackStateInternal(status int) string {
	switch status {
	case portainer.StackStatusActive:
		return projecttypes.PortainerStackStateActive
	case portainer.StackStatusInactive:
		return projecttypes.PortainerStackStateInactive
	default:
		return projecttypes.PortainerStackStateUnknown
	}
}

// portainerEnvContentInternal turns Portainer's stack variables into .env
// content, keeping Portainer's order so the file reads like the stack did.
func portainerEnvContentInternal(pairs []portainer.Pair) *string {
	var builder strings.Builder
	for _, pair := range pairs {
		name := strings.TrimSpace(pair.Name)
		if name == "" {
			continue
		}
		builder.WriteString(name)
		builder.WriteByte('=')
		builder.WriteString(projects.FormatEnvValue(pair.Value))
		builder.WriteByte('\n')
	}

	if builder.Len() == 0 {
		return nil
	}

	content := builder.String()
	return &content
}
