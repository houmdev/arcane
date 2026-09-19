<!--
	portainer-import-dialog — connects to a Portainer instance, lists its stacks,
	and imports the selected ones as Arcane projects. The dialog walks three
	steps: connect, select, and the per-stack import outcome.
-->
<script lang="ts">
	import { ResponsiveDialog } from '#lib/components/ui/responsive-dialog/index.js';
	import { ArcaneButton } from '#lib/components/arcane-button/index.js';
	import { Badge } from '#lib/components/ui/badge/index.js';
	import { Checkbox } from '#lib/components/ui/checkbox/index.js';
	import * as Alert from '#lib/components/ui/alert/index.js';
	import * as Tabs from '#lib/components/ui/tabs/index.js';
	import FormInput from '#lib/components/form/form-input.svelte';
	import LabeledSwitch from '#lib/components/form/labeled-switch.svelte';
	import { AlertTriangleIcon, CheckIcon, CloseIcon } from '#lib/icons/index.js';
	import { m } from '#lib/paraglide/messages.js';
	import { projectService } from '#lib/services/project-service.js';
	import type {
		PortainerConnection,
		PortainerImportResult,
		PortainerStack,
		PortainerStackKind,
		PortainerStackState
	} from '#lib/types/portainer.js';
	import { handleApiResultWithCallbacks } from '#lib/utils/api.js';
	import { createForm, preventDefault } from '#lib/utils/settings.svelte.js';
	import { tryCatch } from '#lib/utils/try-catch.js';
	import { SvelteSet } from 'svelte/reactivity';
	import { z } from 'zod/v4';

	let {
		open = $bindable(false),
		onImported
	}: {
		open: boolean;
		onImported: (result: PortainerImportResult) => void | Promise<void>;
	} = $props();

	const formSchema = z
		.object({
			url: z.string().min(1, m.portainer_url_required()),
			authMode: z.enum(['token', 'credentials']),
			accessToken: z.string(),
			username: z.string(),
			password: z.string(),
			skipTlsVerify: z.boolean()
		})
		.superRefine((data, ctx) => {
			if (data.authMode === 'token') {
				if (!data.accessToken.trim()) {
					ctx.addIssue({ code: z.ZodIssueCode.custom, message: m.portainer_access_token_required(), path: ['accessToken'] });
				}
				return;
			}
			if (!data.username.trim()) {
				ctx.addIssue({ code: z.ZodIssueCode.custom, message: m.common_username_required(), path: ['username'] });
			}
			if (!data.password) {
				ctx.addIssue({ code: z.ZodIssueCode.custom, message: m.common_password_required(), path: ['password'] });
			}
		});

	const emptyForm = {
		url: '',
		authMode: 'token' as const,
		accessToken: '',
		username: '',
		password: '',
		skipTlsVerify: false
	};

	const form = createForm<typeof formSchema>(formSchema, emptyForm);
	const inputs = $derived(form.inputs);

	let step = $state<'connect' | 'select' | 'result'>('connect');
	let connecting = $state(false);
	let importing = $state(false);
	let stacks = $state<PortainerStack[]>([]);
	let portainerVersion = $state('');
	let connection = $state<PortainerConnection | null>(null);
	let importResult = $state<PortainerImportResult | null>(null);
	const selectedStackIds = new SvelteSet<number>();

	const importableStacks = $derived(stacks.filter((stack) => stack.importable));
	const selectedCount = $derived(importableStacks.filter((stack) => selectedStackIds.has(stack.id)).length);
	const allSelected = $derived(importableStacks.length > 0 && selectedCount === importableStacks.length);

	const stackKindLabels: Record<PortainerStackKind, () => string> = {
		compose: m.portainer_kind_compose,
		swarm: m.portainer_kind_swarm,
		kubernetes: m.portainer_kind_kubernetes,
		unknown: m.common_unknown
	};

	const stackStateLabels: Record<PortainerStackState, () => string> = {
		active: m.common_running,
		inactive: m.common_stopped,
		unknown: m.common_unknown
	};

	function resetDialog() {
		form.reset(emptyForm);
		step = 'connect';
		stacks = [];
		portainerVersion = '';
		connection = null;
		importResult = null;
		selectedStackIds.clear();
	}

	function handleOpenChange(nextOpen: boolean) {
		open = nextOpen;
		if (!nextOpen) resetDialog();
	}

	function toggleStack(stackId: number, selected: boolean) {
		if (selected) {
			selectedStackIds.add(stackId);
			return;
		}
		selectedStackIds.delete(stackId);
	}

	function toggleAllStacks(selected: boolean) {
		selectedStackIds.clear();
		if (!selected) return;
		for (const stack of importableStacks) selectedStackIds.add(stack.id);
	}

	async function handleConnect() {
		const data = form.validate();
		if (!data) return;

		const nextConnection: PortainerConnection = {
			url: data.url,
			skipTlsVerify: data.skipTlsVerify,
			...(data.authMode === 'token' ? { accessToken: data.accessToken } : { username: data.username, password: data.password })
		};

		connecting = true;
		await handleApiResultWithCallbacks({
			result: await tryCatch(projectService.listPortainerStacks(nextConnection)),
			message: m.portainer_connect_failed(),
			setLoadingState: (value) => (connecting = value),
			onSuccess: (list) => {
				connection = nextConnection;
				stacks = list.stacks;
				portainerVersion = list.portainerVersion ?? '';
				selectedStackIds.clear();
				for (const stack of list.stacks) {
					if (stack.importable && !stack.existingProjectId) selectedStackIds.add(stack.id);
				}
				step = 'select';
			}
		});
	}

	async function handleImport() {
		const activeConnection = connection;
		if (!activeConnection) return;

		const stackIds = importableStacks.filter((stack) => selectedStackIds.has(stack.id)).map((stack) => stack.id);
		if (stackIds.length === 0) return;

		importing = true;
		await handleApiResultWithCallbacks({
			result: await tryCatch(projectService.importPortainerStacks({ connection: activeConnection, stackIds })),
			message: m.portainer_import_failed(),
			setLoadingState: (value) => (importing = value),
			onSuccess: async (result) => {
				importResult = result;
				step = 'result';
				await onImported(result);
			}
		});
	}
</script>

{#snippet connectStep()}
	<form onsubmit={preventDefault(handleConnect)} class="grid gap-4 py-2">
		<FormInput
			label={m.portainer_url_label()}
			placeholder={m.portainer_url_placeholder()}
			description={m.portainer_url_description()}
			disabled={connecting}
			bind:input={inputs.url}
		/>

		<Tabs.Root bind:value={inputs.authMode.value} class="w-full">
			<Tabs.List class="grid w-full grid-cols-2">
				<Tabs.Trigger value="token">{m.portainer_auth_token_tab()}</Tabs.Trigger>
				<Tabs.Trigger value="credentials">{m.portainer_auth_credentials_tab()}</Tabs.Trigger>
			</Tabs.List>

			<Tabs.Content value="token" class="mt-4">
				<FormInput
					label={m.portainer_access_token_label()}
					type="password"
					description={m.portainer_access_token_description()}
					disabled={connecting}
					bind:input={inputs.accessToken}
				/>
			</Tabs.Content>

			<Tabs.Content value="credentials" class="mt-4 grid gap-4">
				<FormInput label={m.common_username()} disabled={connecting} bind:input={inputs.username} />
				<FormInput label={m.common_password()} type="password" disabled={connecting} bind:input={inputs.password} />
			</Tabs.Content>
		</Tabs.Root>

		<LabeledSwitch
			id="portainer-skip-tls-verify"
			label={m.portainer_skip_tls_verify_label()}
			description={m.portainer_skip_tls_verify_description()}
			disabled={connecting}
			bind:checked={inputs.skipTlsVerify.value}
		/>
	</form>
{/snippet}

{#snippet stackRow(stack: PortainerStack)}
	{@const selected = selectedStackIds.has(stack.id)}
	<label
		class="flex items-start gap-3 rounded-md border p-3 {stack.importable ? 'cursor-pointer hover:bg-accent/40' : 'opacity-70'}"
	>
		<Checkbox
			checked={selected}
			disabled={!stack.importable || importing}
			onCheckedChange={(checked) => toggleStack(stack.id, checked === true)}
			aria-label={stack.name}
		/>
		<div class="grid min-w-0 flex-1 gap-1">
			<div class="flex flex-wrap items-center gap-2">
				<span class="truncate text-sm font-medium">{stack.name}</span>
				<Badge variant="outline" size="sm">{stackKindLabels[stack.kind]()}</Badge>
				<Badge variant={stack.state === 'active' ? 'green' : 'gray'} size="sm">
					{stackStateLabels[stack.state]()}
				</Badge>
			</div>
			<p class="text-xs text-muted-foreground">
				{#if stack.endpointName}
					{m.portainer_stack_meta_with_endpoint({ endpoint: stack.endpointName, count: stack.envCount })}
				{:else}
					{m.portainer_stack_meta({ count: stack.envCount })}
				{/if}
			</p>
			{#if stack.skipReason}
				<p class="text-xs text-muted-foreground">{stack.skipReason}</p>
			{:else if stack.existingProjectId}
				<p class="text-xs text-amber-600 dark:text-amber-400">{m.portainer_stack_name_taken()}</p>
			{/if}
		</div>
	</label>
{/snippet}

{#snippet selectStep()}
	<div class="grid gap-3 py-2">
		<Alert.Root>
			<Alert.Description>
				{#if portainerVersion}
					{m.portainer_connected_with_version({ url: connection?.url ?? '', version: portainerVersion })}
				{:else}
					{m.portainer_connected({ url: connection?.url ?? '' })}
				{/if}
			</Alert.Description>
		</Alert.Root>

		{#if stacks.length === 0}
			<p class="py-6 text-center text-sm text-muted-foreground">{m.portainer_no_stacks()}</p>
		{:else}
			<label class="flex cursor-pointer items-center gap-3 px-1 text-sm font-medium">
				<Checkbox
					checked={allSelected}
					indeterminate={selectedCount > 0 && !allSelected}
					disabled={importableStacks.length === 0 || importing}
					onCheckedChange={(checked) => toggleAllStacks(checked === true)}
					aria-label={m.common_select_all()}
				/>
				{m.common_select_all()}
			</label>

			<div class="grid max-h-[45vh] gap-2 overflow-y-auto pr-1">
				{#each stacks as stack (stack.id)}
					{@render stackRow(stack)}
				{/each}
			</div>

			<p class="text-xs text-muted-foreground">{m.portainer_import_hint()}</p>
		{/if}
	</div>
{/snippet}

{#snippet resultStep()}
	<div class="grid gap-3 py-2">
		{#if importResult}
			<Alert.Root variant={importResult.failed > 0 ? 'warning' : 'default'}>
				{#if importResult.failed > 0}
					<AlertTriangleIcon class="size-4" />
				{:else}
					<CheckIcon class="size-4" />
				{/if}
				<Alert.Description>
					{m.portainer_import_summary({ imported: importResult.imported, failed: importResult.failed })}
				</Alert.Description>
			</Alert.Root>

			<div class="grid max-h-[45vh] gap-2 overflow-y-auto pr-1">
				{#each importResult.stacks as stack (stack.stackId)}
					<div class="flex items-start gap-3 rounded-md border p-3">
						{#if stack.imported}
							<CheckIcon class="mt-0.5 size-4 shrink-0 text-emerald-500" />
						{:else}
							<CloseIcon class="mt-0.5 size-4 shrink-0 text-red-500" />
						{/if}
						<div class="grid min-w-0 flex-1 gap-1">
							<span class="truncate text-sm font-medium">{stack.stackName || `#${stack.stackId}`}</span>
							{#if stack.imported}
								<span class="text-xs text-muted-foreground">
									{m.portainer_imported_as({ project: stack.projectName ?? '' })}
								</span>
							{:else if stack.error}
								<span class="text-xs text-red-600 dark:text-red-400">{stack.error}</span>
							{/if}
						</div>
					</div>
				{/each}
			</div>
		{/if}
	</div>
{/snippet}

<ResponsiveDialog
	{open}
	onOpenChange={handleOpenChange}
	title={m.portainer_import_title()}
	description={m.portainer_import_description()}
	contentClass="sm:max-w-2xl"
>
	{#snippet children()}
		{#if step === 'connect'}
			{@render connectStep()}
		{:else if step === 'select'}
			{@render selectStep()}
		{:else}
			{@render resultStep()}
		{/if}
	{/snippet}

	{#snippet footer()}
		<div class="flex w-full justify-end gap-2">
			{#if step === 'connect'}
				<ArcaneButton
					action="base"
					tone="ghost"
					onclick={() => handleOpenChange(false)}
					customLabel={m.common_cancel()}
					disabled={connecting}
				/>
				<ArcaneButton
					action="base"
					onclick={handleConnect}
					loading={connecting}
					customLabel={m.common_connect()}
					loadingLabel={m.portainer_connecting()}
					disabled={connecting}
				/>
			{:else if step === 'select'}
				<ArcaneButton
					action="base"
					tone="ghost"
					onclick={() => (step = 'connect')}
					customLabel={m.common_back()}
					disabled={importing}
				/>
				<ArcaneButton
					action="create"
					onclick={handleImport}
					loading={importing}
					customLabel={m.portainer_import_selected({ count: selectedCount })}
					loadingLabel={m.portainer_importing()}
					disabled={importing || selectedCount === 0}
				/>
			{:else}
				<ArcaneButton action="base" onclick={() => handleOpenChange(false)} customLabel={m.common_done()} />
			{/if}
		</div>
	{/snippet}
</ResponsiveDialog>
