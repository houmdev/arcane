package project

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"emperror.dev/errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/samber/mo"

	"github.com/getarcaneapp/arcane/backend/v2/internal/common"
	"github.com/getarcaneapp/arcane/backend/v2/internal/database"
	"github.com/getarcaneapp/arcane/backend/v2/internal/middleware"
	"github.com/getarcaneapp/arcane/backend/v2/pkg/authz"
	activitylib "github.com/getarcaneapp/arcane/backend/v2/pkg/libarcane/activity"
	"github.com/getarcaneapp/arcane/backend/v2/pkg/utils"
	"github.com/getarcaneapp/arcane/backend/v2/pkg/utils/handlerutil"
	activitytypes "github.com/getarcaneapp/arcane/types/v2/activity"
	"github.com/getarcaneapp/arcane/types/v2/base"
	projecttypes "github.com/getarcaneapp/arcane/types/v2/project"
)

// ListPortainerStacksInput carries the Portainer connection to discover stacks on.
type ListPortainerStacksInput struct {
	EnvironmentID string `path:"id" doc:"Environment ID"`
	Body          projecttypes.PortainerConnection
}

// ImportPortainerStacksInput carries the Portainer connection and the stacks to import.
type ImportPortainerStacksInput struct {
	EnvironmentID string `path:"id" doc:"Environment ID"`
	Body          projecttypes.PortainerImport
}

func registerProjectPortainerRoutesInternal(api huma.API, h *ProjectHandler) {
	middleware.RegisterWithPermission(api, huma.Operation{
		OperationID: "list-portainer-stacks",
		Method:      http.MethodPost,
		Path:        "/environments/{id}/projects/portainer/stacks",
		Summary:     "List Portainer stacks",
		Description: "Discover the stacks on a Portainer instance and report which ones can be imported as projects",
		Tags:        []string{"Projects"},
		Security:    handlerutil.DefaultOperationSecurity(),
	}, authz.PermProjectsCreate, h.ListPortainerStacks)

	middleware.RegisterWithPermission(api, huma.Operation{
		OperationID: "import-portainer-stacks",
		Method:      http.MethodPost,
		Path:        "/environments/{id}/projects/portainer/import",
		Summary:     "Import Portainer stacks",
		Description: "Import selected Portainer stacks as Docker Compose projects",
		Tags:        []string{"Projects"},
		Security:    handlerutil.DefaultOperationSecurity(),
	}, authz.PermProjectsCreate, h.ImportPortainerStacks)
}

// ListPortainerStacks discovers the stacks on a Portainer instance.
func (h *ProjectHandler) ListPortainerStacks(ctx context.Context, input *ListPortainerStacksInput) (*handlerutil.Out[projecttypes.PortainerStackList], error) {
	stacks, err := h.projectService.ListPortainerStacks(ctx, input.Body)
	if err != nil {
		return nil, portainerHTTPErrorInternal(err, "Failed to list Portainer stacks")
	}

	return &handlerutil.Out[projecttypes.PortainerStackList]{
		Body: base.ApiResponse[projecttypes.PortainerStackList]{
			Success: true,
			Data:    *stacks,
		},
	}, nil
}

// ImportPortainerStacks imports the selected Portainer stacks as projects.
func (h *ProjectHandler) ImportPortainerStacks(ctx context.Context, input *ImportPortainerStacksInput) (*handlerutil.Out[projecttypes.PortainerImportResult], error) {
	user, err := handlerutil.RequireUser(ctx)
	if err != nil {
		return nil, err
	}

	var result *projecttypes.PortainerImportResult
	runtimeCtx := utils.ActivityRuntimeContext(ctx, h.appCtx)
	activityID, err := activitylib.RunHandlerActivity(runtimeCtx, h.activityService, activitylib.HandlerOptions{
		EnvironmentID:  input.EnvironmentID,
		Type:           activitytypes.TypeResourceAction,
		ResourceType:   "project",
		ResourceID:     "portainer-import",
		ResourceName:   "Portainer import",
		User:           user,
		Step:           "Importing Portainer stacks",
		Message:        "Importing " + strconv.Itoa(len(input.Body.StackIDs)) + " Portainer stack(s)",
		SuccessMessage: "Portainer stacks imported",
		Metadata:       database.JSON{"action": "import_portainer_stacks", "stackCount": len(input.Body.StackIDs)},
	}, func(runtimeCtx context.Context) error {
		var importErr error
		result, importErr = h.projectService.ImportPortainerStacks(runtimeCtx, input.Body, *user)
		return importErr
	})
	if err != nil {
		return nil, portainerHTTPErrorInternal(err, "Failed to import Portainer stacks")
	}

	result.ActivityID = mo.EmptyableToOption(strings.TrimSpace(activityID)).ToPointer()

	return &handlerutil.Out[projecttypes.PortainerImportResult]{
		Body: base.ApiResponse[projecttypes.PortainerImportResult]{
			Success: true,
			Data:    *result,
		},
	}, nil
}

// portainerHTTPErrorInternal maps Portainer failures to HTTP status codes.
// Credentials rejected by Portainer stay a bad request: they are this request's
// input, not the caller's own Arcane session.
func portainerHTTPErrorInternal(err error, fallbackMessage string) error {
	switch {
	case errors.Is(err, common.ErrBadRequest):
		return huma.Error400BadRequest(err.Error())
	case errors.Is(err, common.ErrNotFound):
		return huma.Error404NotFound(err.Error())
	case errors.Is(err, common.ErrUnavailable):
		return huma.Error502BadGateway(err.Error())
	default:
		return huma.Error500InternalServerError(errors.WithMessage(err, fallbackMessage).Error())
	}
}
