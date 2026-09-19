export type PortainerStackKind = 'compose' | 'swarm' | 'kubernetes' | 'unknown';

export type PortainerStackState = 'active' | 'inactive' | 'unknown';

export interface PortainerConnection {
	url: string;
	accessToken?: string;
	username?: string;
	password?: string;
	skipTlsVerify?: boolean;
}

export interface PortainerStack {
	id: number;
	name: string;
	kind: PortainerStackKind;
	state: PortainerStackState;
	endpointName?: string;
	envCount: number;
	importable: boolean;
	skipReason?: string;
	existingProjectId?: string;
}

export interface PortainerStackList {
	stacks: PortainerStack[];
	portainerVersion?: string;
}

export interface PortainerImportRequest {
	connection: PortainerConnection;
	stackIds: number[];
}

export interface PortainerImportedStack {
	stackId: number;
	stackName: string;
	imported: boolean;
	projectId?: string;
	projectName?: string;
	error?: string;
}

export interface PortainerImportResult {
	stacks: PortainerImportedStack[];
	imported: number;
	failed: number;
	activityId?: string;
}
