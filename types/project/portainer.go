package project

// Portainer stack kinds reported by a discovery request.
const (
	PortainerStackKindCompose    = "compose"
	PortainerStackKindSwarm      = "swarm"
	PortainerStackKindKubernetes = "kubernetes"
	PortainerStackKindUnknown    = "unknown"
)

// Portainer stack states reported by a discovery request.
const (
	PortainerStackStateActive   = "active"
	PortainerStackStateInactive = "inactive"
	PortainerStackStateUnknown  = "unknown"
)

// PortainerConnection locates and authenticates against a Portainer instance.
// Either an access token or a username and password must be supplied.
type PortainerConnection struct {
	// URL is the base URL of the Portainer instance, such as https://portainer.example.com.
	//
	// Required: true
	URL string `json:"url" binding:"required"`

	// AccessToken authenticates as a Portainer API access token.
	//
	// Required: false
	AccessToken string `json:"accessToken,omitempty"`

	// Username authenticates with Password when no access token is supplied.
	//
	// Required: false
	Username string `json:"username,omitempty"`

	// Password authenticates with Username when no access token is supplied.
	//
	// Required: false
	Password string `json:"password,omitempty"`

	// SkipTLSVerify accepts self-signed certificates from the Portainer instance.
	//
	// Required: false
	SkipTLSVerify bool `json:"skipTlsVerify,omitempty"`
}

// PortainerStack describes a stack discovered on a Portainer instance.
type PortainerStack struct {
	// ID is the stack's Portainer identifier.
	//
	// Required: true
	ID int `json:"id"`

	// Name is the stack name in Portainer.
	//
	// Required: true
	Name string `json:"name"`

	// Kind is the stack kind: compose, swarm, kubernetes, or unknown.
	//
	// Required: true
	Kind string `json:"kind"`

	// State is the stack's Portainer state: active, inactive, or unknown.
	//
	// Required: true
	State string `json:"state"`

	// EndpointName is the Portainer environment the stack is deployed to.
	//
	// Required: false
	EndpointName string `json:"endpointName,omitempty"`

	// EnvCount is the number of environment variables Portainer stores for the stack.
	//
	// Required: true
	EnvCount int `json:"envCount"`

	// Importable reports whether Arcane can import the stack as a project.
	//
	// Required: true
	Importable bool `json:"importable"`

	// SkipReason explains why an unimportable stack cannot be imported.
	//
	// Required: false
	SkipReason string `json:"skipReason,omitempty"`

	// ExistingProjectID is the project that already uses the stack's name, if any.
	//
	// Required: false
	ExistingProjectID string `json:"existingProjectId,omitempty"`
}

// PortainerStackList is the result of discovering stacks on a Portainer instance.
type PortainerStackList struct {
	// Stacks are the discovered stacks, including ones Arcane cannot import.
	//
	// Required: true
	Stacks []PortainerStack `json:"stacks"`

	// PortainerVersion is the version reported by the Portainer instance.
	//
	// Required: false
	PortainerVersion string `json:"portainerVersion,omitempty"`
}

// PortainerImport requests an import of selected Portainer stacks as projects.
type PortainerImport struct {
	// Connection locates the Portainer instance holding the stacks.
	//
	// Required: true
	Connection PortainerConnection `json:"connection"`

	// StackIDs are the Portainer stack identifiers to import.
	//
	// Required: true
	StackIDs []int `json:"stackIds" binding:"required"`
}

// PortainerImportedStack reports the outcome of importing one stack.
type PortainerImportedStack struct {
	// StackID is the imported stack's Portainer identifier.
	//
	// Required: true
	StackID int `json:"stackId"`

	// StackName is the stack name in Portainer.
	//
	// Required: true
	StackName string `json:"stackName"`

	// Imported reports whether the stack became a project.
	//
	// Required: true
	Imported bool `json:"imported"`

	// ProjectID is the created project's identifier.
	//
	// Required: false
	ProjectID string `json:"projectId,omitempty"`

	// ProjectName is the created project's name, which gains a suffix when the
	// stack name is already taken.
	//
	// Required: false
	ProjectName string `json:"projectName,omitempty"`

	// Error explains why a stack was not imported.
	//
	// Required: false
	Error string `json:"error,omitempty"`
}

// PortainerImportResult reports the outcome of an import request.
type PortainerImportResult struct {
	// Stacks holds the per-stack outcome in request order.
	//
	// Required: true
	Stacks []PortainerImportedStack `json:"stacks"`

	// Imported is the number of stacks that became projects.
	//
	// Required: true
	Imported int `json:"imported"`

	// Failed is the number of stacks that could not be imported.
	//
	// Required: true
	Failed int `json:"failed"`

	// ActivityID identifies the activity recorded for the import.
	//
	// Required: false
	ActivityID *string `json:"activityId,omitempty"`
}
