package projects

import (
	"os"
	"strconv"
	"strings"
	"time"

	"emperror.dev/errors"
	"github.com/spf13/cobra"

	"github.com/getarcaneapp/arcane/cli/v2/internal/client"
	"github.com/getarcaneapp/arcane/cli/v2/internal/cmdutil"
	"github.com/getarcaneapp/arcane/cli/v2/internal/output"
	"github.com/getarcaneapp/arcane/cli/v2/internal/types"
	"github.com/getarcaneapp/arcane/types/v2/project"
)

var (
	portainerURL           string
	portainerAccessToken   string
	portainerUsername      string
	portainerPassword      string
	portainerSkipTLSVerify bool
	portainerStacks        []string
	portainerAll           bool
)

var portainerCmd = &cobra.Command{
	Use:   "portainer",
	Short: "Import stacks from a Portainer instance",
}

var portainerListCmd = &cobra.Command{
	Use:          "list",
	Aliases:      []string{"ls"},
	Short:        "List the stacks on a Portainer instance",
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := client.NewFromConfig()
		if err != nil {
			return err
		}

		connection, err := portainerConnectionFromFlagsInternal()
		if err != nil {
			return err
		}

		list, err := portainerStackListInternal(cmd, c, connection)
		if err != nil {
			return err
		}

		if jsonOutput {
			return cmdutil.PrintJSON(list)
		}

		if len(list.Stacks) == 0 {
			output.Info("No stacks found on %s", connection.URL)
			return nil
		}

		if list.PortainerVersion != "" {
			output.Header("Portainer %s at %s", list.PortainerVersion, connection.URL)
		}

		rows := make([][]string, 0, len(list.Stacks))
		for _, stack := range list.Stacks {
			rows = append(rows, []string{
				strconv.Itoa(stack.ID),
				stack.Name,
				stack.Kind,
				stack.State,
				stack.EndpointName,
				output.TintYesNo(portainerYesNoInternal(stack.Importable)),
				portainerStackNoteInternal(stack),
			})
		}
		output.Table([]string{"ID", "NAME", "KIND", "STATE", "ENVIRONMENT", "IMPORTABLE", "NOTE"}, rows)
		return nil
	},
}

var portainerImportCmd = &cobra.Command{
	Use:          "import",
	Short:        "Import Portainer stacks as projects",
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := client.NewFromConfig()
		if err != nil {
			return err
		}

		connection, err := portainerConnectionFromFlagsInternal()
		if err != nil {
			return err
		}
		if !portainerAll && len(portainerStacks) == 0 {
			return errors.New("select stacks with --stack, or import every importable stack with --all")
		}

		list, err := portainerStackListInternal(cmd, c, connection)
		if err != nil {
			return err
		}

		stackIDs, err := portainerSelectedStackIDsInternal(list.Stacks)
		if err != nil {
			return err
		}

		// Importing writes project files and can pull large Compose trees.
		c.SetTimeout(10 * time.Minute)

		response, err := c.PostJSON[project.PortainerImportResult](cmd.Context(), types.ProjectsPortainerImport(c.EnvID()), project.PortainerImport{
			Connection: connection,
			StackIDs:   stackIDs,
		})
		if err != nil {
			return errors.WrapIf(err, "failed to import Portainer stacks")
		}

		result := response.Data
		if jsonOutput {
			return cmdutil.PrintJSON(result)
		}

		for _, stack := range result.Stacks {
			if stack.Imported {
				output.Success("Imported %s as project %s", stack.StackName, stack.ProjectName)
				continue
			}
			output.Warning("Skipped %s: %s", portainerStackLabelInternal(stack), stack.Error)
		}
		output.Info("Imported %d stack(s), %d failed", result.Imported, result.Failed)

		if result.Failed > 0 {
			return errors.Errorf("%d Portainer stack(s) could not be imported", result.Failed)
		}
		return nil
	},
}

func portainerStackListInternal(cmd *cobra.Command, c *client.Client, connection project.PortainerConnection) (project.PortainerStackList, error) {
	response, err := c.PostJSON[project.PortainerStackList](cmd.Context(), types.ProjectsPortainerStacks(c.EnvID()), connection)
	if err != nil {
		return project.PortainerStackList{}, errors.WrapIf(err, "failed to list Portainer stacks")
	}
	return response.Data, nil
}

// portainerConnectionFromFlagsInternal builds the connection, falling back to
// environment variables so secrets stay out of shell history.
func portainerConnectionFromFlagsInternal() (project.PortainerConnection, error) {
	connection := project.PortainerConnection{
		URL:           strings.TrimSpace(portainerURL),
		AccessToken:   strings.TrimSpace(portainerAccessToken),
		Username:      strings.TrimSpace(portainerUsername),
		Password:      portainerPassword,
		SkipTLSVerify: portainerSkipTLSVerify,
	}

	if connection.AccessToken == "" {
		connection.AccessToken = strings.TrimSpace(os.Getenv("ARCANE_PORTAINER_TOKEN"))
	}
	if connection.Password == "" {
		connection.Password = os.Getenv("ARCANE_PORTAINER_PASSWORD")
	}

	if connection.URL == "" {
		return connection, errors.New("--url is required")
	}
	if connection.AccessToken == "" && (connection.Username == "" || connection.Password == "") {
		return connection, errors.New("provide --access-token, or --username with --password")
	}

	return connection, nil
}

// portainerSelectedStackIDsInternal resolves --stack values, which may be
// Portainer stack IDs or names, against the discovered stacks.
func portainerSelectedStackIDsInternal(stacks []project.PortainerStack) ([]int, error) {
	if portainerAll {
		stackIDs := make([]int, 0, len(stacks))
		for _, stack := range stacks {
			if stack.Importable {
				stackIDs = append(stackIDs, stack.ID)
			}
		}
		if len(stackIDs) == 0 {
			return nil, errors.New("no importable stacks were found on the Portainer instance")
		}
		return stackIDs, nil
	}

	stackIDs := make([]int, 0, len(portainerStacks))
	for _, identifier := range portainerStacks {
		stack, err := portainerMatchStackInternal(stacks, identifier)
		if err != nil {
			return nil, err
		}
		if !stack.Importable {
			return nil, errors.Errorf("stack %q cannot be imported: %s", stack.Name, stack.SkipReason)
		}
		stackIDs = append(stackIDs, stack.ID)
	}
	return stackIDs, nil
}

func portainerMatchStackInternal(stacks []project.PortainerStack, identifier string) (project.PortainerStack, error) {
	trimmed := strings.TrimSpace(identifier)
	var matches []project.PortainerStack
	for _, stack := range stacks {
		if strconv.Itoa(stack.ID) == trimmed || strings.EqualFold(stack.Name, trimmed) {
			matches = append(matches, stack)
		}
	}

	switch len(matches) {
	case 0:
		return project.PortainerStack{}, errors.Errorf("stack %q was not found on the Portainer instance", trimmed)
	case 1:
		return matches[0], nil
	default:
		return project.PortainerStack{}, errors.Errorf("stack %q matches %d stacks; use the stack ID instead", trimmed, len(matches))
	}
}

func portainerStackNoteInternal(stack project.PortainerStack) string {
	if stack.SkipReason != "" {
		return stack.SkipReason
	}
	if stack.ExistingProjectID != "" {
		return "A project with this name already exists"
	}
	return ""
}

func portainerStackLabelInternal(stack project.PortainerImportedStack) string {
	if stack.StackName != "" {
		return stack.StackName
	}
	return "stack " + strconv.Itoa(stack.StackID)
}

func portainerYesNoInternal(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func init() {
	ProjectsCmd.AddCommand(portainerCmd)
	portainerCmd.AddCommand(portainerListCmd)
	portainerCmd.AddCommand(portainerImportCmd)

	for _, cmd := range []*cobra.Command{portainerListCmd, portainerImportCmd} {
		cmd.Flags().StringVar(&portainerURL, "url", "", "Portainer base URL (e.g. https://portainer.example.com)")
		cmd.Flags().StringVar(&portainerAccessToken, "access-token", "", "Portainer access token (or ARCANE_PORTAINER_TOKEN)")
		cmd.Flags().StringVar(&portainerUsername, "username", "", "Portainer username, used without an access token")
		cmd.Flags().StringVar(&portainerPassword, "password", "", "Portainer password (or ARCANE_PORTAINER_PASSWORD)")
		cmd.Flags().BoolVar(&portainerSkipTLSVerify, "skip-tls-verify", false, "Accept self-signed certificates from the Portainer instance")
		cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output in JSON format")
		_ = cmd.MarkFlagRequired("url")
	}

	portainerImportCmd.Flags().StringArrayVar(&portainerStacks, "stack", nil, "Stack ID or name to import (repeatable)")
	portainerImportCmd.Flags().BoolVar(&portainerAll, "all", false, "Import every importable stack")
}
