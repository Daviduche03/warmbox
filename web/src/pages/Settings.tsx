import { useEffect, useState, type FormEvent } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import {
	Dialog,
	DialogContent,
	DialogDescription,
	DialogFooter,
	DialogHeader,
	DialogTitle,
} from "@/components/ui/dialog";
import { DropdownMenuItem } from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import {
	Select,
	SelectContent,
	SelectItem,
	SelectTrigger,
	SelectValue,
} from "@/components/ui/select";
import { Separator } from "@/components/ui/separator";
import {
	Table,
	TableBody,
	TableCell,
	TableHead,
	TableHeader,
	TableRow,
} from "@/components/ui/table";
import {
	Tabs,
	TabsContent,
	TabsList,
	TabsTrigger,
} from "@/components/ui/tabs";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { NoticeLine } from "@/components/notice-line";
import { RowActions } from "@/components/row-actions";
import { SectionHead } from "@/components/section-head";
import { Spinner } from "@/components/spinner";
import { StatusIndicator } from "@/components/indicator";
import { useStore } from "@/lib/store";
import { api, roleRank, ApiError, type Member } from "@/lib/api";
import { usePending } from "@/lib/use-pending";
import {
	ArrowsClockwise,
	Check as CheckIcon,
	Copy as CopyIcon,
	Plus as PlusIcon,
	Trash as TrashIcon,
} from "@phosphor-icons/react";

/** Flush notice for bare content — no card bleed, no trailing rule. */
function Notice({ message, tone }: { message?: string; tone?: "error" | "success" }) {
	return (
		<NoticeLine
			className="border-b-0 px-0 py-0"
			message={message}
			tone={tone}
		/>
	);
}

function Row({ label, value }: { label: string; value: string }) {
	return (
		<div className="flex items-center justify-between gap-4 border-b px-6 py-3 last:border-b-0">
			<span className="text-muted-foreground text-sm">{label}</span>
			<span className="truncate font-medium text-sm tabular-nums">{value}</span>
		</div>
	);
}

function useCopy(): [boolean, (text: string) => Promise<void>] {
	const [copied, setCopied] = useState(false);
	async function copy(text: string) {
		if (!text) return;
		try {
			await navigator.clipboard.writeText(text);
			setCopied(true);
			window.setTimeout(() => setCopied(false), 1600);
		} catch {
			/* clipboard unavailable */
		}
	}
	return [copied, copy];
}

function AccountSection() {
	const { me, refresh } = useStore();
	const [current, setCurrent] = useState("");
	const [next, setNext] = useState("");
	const [confirm, setConfirm] = useState("");
	const [error, setError] = useState<string>();
	const [done, setDone] = useState<string>();
	const { pending, run } = usePending();

	async function signOut() {
		try {
			await api.auth.logout();
		} catch {
			/* session may already be gone */
		}
		await refresh();
	}

	async function changePassword(e: FormEvent) {
		e.preventDefault();
		if (next !== confirm) {
			setError("New passwords do not match.");
			return;
		}
		setError(undefined);
		setDone(undefined);
		await run(async () => {
			try {
				await api.auth.changePassword({ current, next });
				setCurrent("");
				setNext("");
				setConfirm("");
				setDone("Password updated.");
			} catch (err) {
				setError(err instanceof ApiError ? err.message : "update failed");
			}
		});
	}

	return (
		<div className="max-w-2xl space-y-6">
			<SectionHead
				action={
					<Button onClick={() => void signOut()} size="sm" variant="outline">
						Sign out
					</Button>
				}
				desc={
					me ? (
						<>
							Signed in as <span className="text-foreground">{me.user.name}</span>{" "}
							({me.user.email}) · {me.role} of {me.workspace.name}.
						</>
					) : (
						"Your session."
					)
				}
				title="Account"
			/>
			<Card className="shadow-none dark:ring-0">
				<CardContent className="px-6 py-2">
					<form className="space-y-4" onSubmit={(e) => void changePassword(e)}>
						<p className="font-medium text-sm">Change password</p>
						<Input
							aria-label="Current password"
							autoComplete="current-password"
							disabled={pending}
							onChange={(e) => setCurrent(e.target.value)}
							placeholder="Current password"
							type="password"
							value={current}
						/>
						<div className="flex flex-col gap-3 sm:flex-row">
							<Input
								aria-label="New password"
								autoComplete="new-password"
								disabled={pending}
								onChange={(e) => setNext(e.target.value)}
								placeholder="New password (8+ characters)"
								type="password"
								value={next}
							/>
							<Input
								aria-label="Confirm new password"
								autoComplete="new-password"
								disabled={pending}
								onChange={(e) => setConfirm(e.target.value)}
								placeholder="Confirm"
								type="password"
								value={confirm}
							/>
						</div>
						<Notice message={error} />
						<Notice message={done} tone="success" />
						<Button
							aria-busy={pending}
							disabled={!current || next.length < 8 || pending}
							size="sm"
							type="submit"
							variant="outline"
						>
							{pending ? (
								<>
									<Spinner />
									Updating…
								</>
							) : (
								"Update password"
							)}
						</Button>
					</form>
				</CardContent>
			</Card>
		</div>
	);
}

function TokensSection() {
	const [tokens, setTokens] = useState<Array<{
		id: string;
		name: string;
		prefix: string;
		created_at: string;
		last_used_at: string | null;
	}>>([]);
	const [name, setName] = useState("");
	const [createOpen, setCreateOpen] = useState(false);
	const [createError, setCreateError] = useState<string>();
	const [created, setCreated] = useState<{ token: string; id: string } | null>(null);
	const [error, setError] = useState<string>();
	const [copied, copy] = useCopy();
	const [pendingDelete, setPendingDelete] = useState<string>();
	const { pending, run } = usePending();

	async function load() {
		try {
			const res = await api.tokens.list();
			setTokens(res.tokens);
		} catch {
			/* listed on mount; failures surface on action */
		}
	}

	useEffect(() => {
		void load();
	}, []);

	async function create(e: FormEvent) {
		e.preventDefault();
		if (!name.trim() || pending) return;
		setCreateError(undefined);
		await run(async () => {
			try {
				const res = await api.tokens.create(name.trim());
				setCreated({ token: res.token, id: res.id });
				setName("");
				setCreateOpen(false);
				await load();
			} catch (err) {
				setCreateError(
					err instanceof ApiError ? err.message : "create failed",
				);
			}
		});
	}

	async function revoke(id: string) {
		setError(undefined);
		try {
			await api.tokens.revoke(id);
			await load();
		} catch (err) {
			setError(err instanceof ApiError ? err.message : "revoke failed");
		}
	}

	return (
		<div className="max-w-2xl space-y-8">
			<section className="space-y-4">
				<SectionHead
					action={
						<Button onClick={() => setCreateOpen(true)} size="sm">
							<PlusIcon />
							New token
						</Button>
					}
					desc={
						<>
							Long-lived credentials for CLI and scripts. Send one as{" "}
							<code className="font-mono">Authorization: Bearer &lt;token&gt;</code>.
						</>
					}
					title="API tokens"
				/>
				<Card className="shadow-none dark:ring-0">
					<CardContent className="p-0">
						<NoticeLine message={error} />
						{created ? (
							<div className="space-y-2 border-b px-6 py-4">
								<p className="text-muted-foreground text-sm">
									Copy it now — it is never shown again.
								</p>
								<div className="flex items-center gap-2">
									<code className="min-w-0 flex-1 truncate rounded-md border bg-muted px-3 py-2 font-mono text-sm">
										{created.token}
									</code>
									<Button
										onClick={() => void copy(created.token)}
										size="sm"
										variant="outline"
									>
										{copied ? <CheckIcon /> : <CopyIcon />}
										{copied ? "Copied" : "Copy"}
									</Button>
								</div>
							</div>
						) : null}
						{tokens.length === 0 ? (
							<p className="text-muted-foreground px-6 py-4 text-sm">
								No tokens yet.
							</p>
						) : (
							<ul className="divide-y divide-border">
								{tokens.map((t) => (
									<li
										className="flex items-center gap-3 px-6 py-2.5"
										key={t.id}
									>
										<div className="min-w-0 flex-1">
											<p className="truncate font-medium text-sm">
												{t.name || "Untitled"}
											</p>
											<p className="font-mono text-muted-foreground text-xs">
												{t.prefix}…
												{t.last_used_at
													? ` · used ${t.last_used_at}`
													: " · never used"}
											</p>
										</div>
										<RowActions label={`Actions for token ${t.name || t.id}`}>
											<DropdownMenuItem
												onSelect={() => setPendingDelete(t.id)}
												variant="destructive"
											>
												<TrashIcon />
												Revoke
											</DropdownMenuItem>
										</RowActions>
									</li>
								))}
							</ul>
						)}
					</CardContent>
				</Card>
				<Dialog
					onOpenChange={(next) => {
						setCreateOpen(next);
						setCreateError(undefined);
					}}
					open={createOpen}
				>
					<DialogContent>
						<DialogHeader>
							<DialogTitle>New API token</DialogTitle>
							<DialogDescription>
								Long-lived credential for the CLI and scripts. You'll see it
								once, right after it's created.
							</DialogDescription>
						</DialogHeader>
						<form
							className="space-y-4"
							onSubmit={(e) => void create(e)}
						>
							<div className="space-y-2">
								<label className="text-sm" htmlFor="new-token-name">
									Name
								</label>
								<Input
									autoFocus
									disabled={pending}
									id="new-token-name"
									onChange={(e) => setName(e.target.value)}
									placeholder="e.g. deploy script"
									value={name}
								/>
							</div>
							<NoticeLine
								className="border-b-0 px-0 py-0"
								message={createError}
							/>
							<DialogFooter>
								<Button
									disabled={pending}
									onClick={() => setCreateOpen(false)}
									size="sm"
									type="button"
									variant="outline"
								>
									Cancel
								</Button>
								<Button
									aria-busy={pending}
									disabled={!name.trim() || pending}
									size="sm"
									type="submit"
								>
									{pending ? (
										<>
											<Spinner />
											Creating…
										</>
									) : (
										<>
											<PlusIcon />
											Create token
										</>
									)}
								</Button>
							</DialogFooter>
						</form>
					</DialogContent>
				</Dialog>
				<ConfirmDialog
					description="Scripts using it stop authenticating immediately."
					onConfirm={async () => {
						if (pendingDelete) await revoke(pendingDelete);
					}}
					onOpenChange={(next) => {
						if (!next) setPendingDelete(undefined);
					}}
					open={!!pendingDelete}
					title="Revoke this token?"
				/>
			</section>

			<Separator />

			<section className="space-y-4">
				<SectionHead
					as="h2"
					desc="Every endpoint below needs a session cookie or a personal token from this tab."
					title="API access"
				/>
				<Card className="shadow-none dark:ring-0">
					<CardContent className="px-6 py-2">
						<div className="space-y-4">
							<p className="text-muted-foreground text-sm">
								Send it as{" "}
								<code className="font-mono">
									Authorization: Bearer &lt;token&gt;
								</code>{" "}
								or append <code className="font-mono">?token=</code> to a
								console URL (the noVNC frames do).
							</p>
							<ul className="space-y-1.5 font-mono text-xs">
								<li>
									<span className="text-muted-foreground">GET </span>/api/status
								</li>
								<li>
									<span className="text-muted-foreground">GET </span>/api/desktops
								</li>
								<li>
									<span className="text-muted-foreground">POST </span>/api/desktops
								</li>
								<li>
									<span className="text-muted-foreground">GET </span>/api/volumes
								</li>
								<li>
									<span className="text-muted-foreground">GET </span>/api/snapshots
								</li>
								<li>
									<span className="text-muted-foreground">GET </span>/api/images
								</li>
								<li>
									<span className="text-muted-foreground">POST </span>
									/api/desktops/&#123;id&#125;/exec
								</li>
							</ul>
						</div>
					</CardContent>
				</Card>
			</section>
		</div>
	);
}

const MANAGEABLE_ROLES = ["viewer", "member", "admin", "owner"] as const;

function TeamSection() {
	const { me, refresh } = useStore();
	const [members, setMembers] = useState<Member[]>([]);
	const [name, setName] = useState("");
	const [email, setEmail] = useState("");
	const [password, setPassword] = useState("");
	const [role, setRole] = useState<string>("member");
	const [inviteOpen, setInviteOpen] = useState(false);
	const [inviteError, setInviteError] = useState<string>();
	const [error, setError] = useState<string>();
	const [notice, setNotice] = useState<string>();
	const [pendingDelete, setPendingDelete] = useState<string>();
	const { pending, run } = usePending();

	const myRank = roleRank(me?.role);
	const canManage = myRank >= roleRank("admin");

	async function load() {
		try {
			const res = await api.users.list();
			setMembers(res.users);
		} catch (err) {
			setError(err instanceof ApiError ? err.message : "could not load team");
		}
	}

	useEffect(() => {
		if (canManage) void load();
	}, [canManage]);

	if (!canManage) return null;

	async function invite(e: FormEvent) {
		e.preventDefault();
		if (pending) return;
		setInviteError(undefined);
		await run(async () => {
			try {
				await api.users.create({
					name: name.trim(),
					email: email.trim(),
					password,
					role: role as "viewer" | "member" | "admin" | "owner",
				});
				setName("");
				setEmail("");
				setPassword("");
				setRole("member");
				setInviteOpen(false);
				setNotice("Account created.");
				await load();
			} catch (err) {
				setInviteError(
					err instanceof ApiError ? err.message : "invite failed",
				);
			}
		});
	}

	async function setMemberRole(id: string, next: string) {
		setError(undefined);
		setNotice(undefined);
		try {
			await api.users.setRole(id, next as "viewer" | "member" | "admin" | "owner");
			await load();
			await refresh();
		} catch (err) {
			setError(err instanceof ApiError ? err.message : "role change failed");
		}
	}

	async function remove(id: string) {
		setError(undefined);
		setNotice(undefined);
		try {
			await api.users.remove(id);
			await load();
		} catch (err) {
			setError(err instanceof ApiError ? err.message : "remove failed");
		}
	}

	return (
		<div className="space-y-6">
			<SectionHead
				action={
					<Button onClick={() => setInviteOpen(true)} size="sm">
						<PlusIcon />
						Invite
					</Button>
				}
				badge={<Badge variant="secondary">{members.length}</Badge>}
				desc={
					<>
						Accounts in {me?.workspace.name}. Owners manage owners; admins
						manage members and viewers.
					</>
				}
				title="Team"
			/>
			<Card className="shadow-none dark:ring-0">
				<CardContent className="p-0">
					<NoticeLine message={error} />
					<NoticeLine message={notice} tone="success" />
					<Table>
						<TableHeader>
							<TableRow className="hover:bg-transparent">
								<TableHead className="pl-6">Account</TableHead>
								<TableHead>Role</TableHead>
								<TableHead className="pr-6 text-right">Actions</TableHead>
							</TableRow>
						</TableHeader>
						<TableBody>
							{members.map((m) => {
								const canTouch =
									m.id !== me?.user.id && roleRank(m.role) < myRank;
								return (
									<TableRow className="hover:bg-transparent" key={m.id}>
										<TableCell className="pl-6">
											<p className="font-medium text-sm">{m.name}</p>
											<p className="text-muted-foreground text-xs">{m.email}</p>
										</TableCell>
										<TableCell>
											{canTouch ? (
												<Select
													onValueChange={(next) => void setMemberRole(m.id, next)}
													value={m.role}
												>
													<SelectTrigger
														aria-label={`Role for ${m.email}`}
														size="sm"
													>
														<SelectValue />
													</SelectTrigger>
													<SelectContent>
														{MANAGEABLE_ROLES.filter(
															(r) => roleRank(r) < myRank
														).map((r) => (
															<SelectItem key={r} value={r}>
																{r}
															</SelectItem>
														))}
													</SelectContent>
												</Select>
											) : (
												<Badge variant="secondary">
													{m.role}
													{m.id === me?.user.id ? " · you" : ""}
												</Badge>
											)}
										</TableCell>
										<TableCell className="pr-6 text-right">
											{canTouch ? (
												<RowActions label={`Actions for ${m.email}`}>
													<DropdownMenuItem
														onSelect={() => setPendingDelete(m.id)}
														variant="destructive"
													>
														<TrashIcon />
														Remove
													</DropdownMenuItem>
												</RowActions>
											) : (
												<span className="text-muted-foreground text-xs">—</span>
											)}
										</TableCell>
									</TableRow>
								);
							})}
						</TableBody>
					</Table>
				</CardContent>
			</Card>
			<Dialog
				onOpenChange={(next) => {
					setInviteOpen(next);
					setInviteError(undefined);
				}}
				open={inviteOpen}
			>
				<DialogContent>
					<DialogHeader>
						<DialogTitle>Invite to team</DialogTitle>
						<DialogDescription>
							Creates an account in {me?.workspace.name}. Give them the
							temporary password — they can change it from Account after they
							sign in.
						</DialogDescription>
					</DialogHeader>
					<form className="space-y-4" onSubmit={(e) => void invite(e)}>
						<div className="space-y-2">
							<label className="text-sm" htmlFor="invite-name">
								Name
							</label>
							<Input
								autoFocus
								disabled={pending}
								id="invite-name"
								onChange={(e) => setName(e.target.value)}
								placeholder="Ada Lovelace"
								value={name}
							/>
						</div>
						<div className="space-y-2">
							<label className="text-sm" htmlFor="invite-email">
								Email
							</label>
							<Input
								autoComplete="email"
								disabled={pending}
								id="invite-email"
								onChange={(e) => setEmail(e.target.value)}
								placeholder="ada@example.com"
								type="email"
								value={email}
							/>
						</div>
						<div className="space-y-2">
							<label className="text-sm" htmlFor="invite-password">
								Temporary password
							</label>
							<Input
								autoComplete="new-password"
								disabled={pending}
								id="invite-password"
								onChange={(e) => setPassword(e.target.value)}
								placeholder="8+ characters"
								type="password"
								value={password}
							/>
						</div>
						<div className="space-y-2">
							<label className="text-sm" htmlFor="invite-role">
								Role
							</label>
							<Select disabled={pending} onValueChange={setRole} value={role}>
								<SelectTrigger
									aria-label="Role"
									className="w-full"
									id="invite-role"
									size="sm"
								>
									<SelectValue />
								</SelectTrigger>
								<SelectContent>
									{MANAGEABLE_ROLES.filter(
										(r) => roleRank(r) < myRank || me?.role === "owner"
									).map((r) => (
										<SelectItem key={r} value={r}>
											{r}
										</SelectItem>
									))}
								</SelectContent>
							</Select>
						</div>
						<NoticeLine
							className="border-b-0 px-0 py-0"
							message={inviteError}
						/>
						<DialogFooter>
							<Button
								disabled={pending}
								onClick={() => setInviteOpen(false)}
								size="sm"
								type="button"
								variant="outline"
							>
								Cancel
							</Button>
							<Button aria-busy={pending} disabled={pending} size="sm" type="submit">
								{pending ? (
									<>
										<Spinner />
										Inviting…
									</>
								) : (
									<>
										<PlusIcon />
										Invite
									</>
								)}
							</Button>
						</DialogFooter>
					</form>
				</DialogContent>
			</Dialog>
			<ConfirmDialog
				description="Their sessions and tokens stop working immediately."
				onConfirm={async () => {
					if (pendingDelete) await remove(pendingDelete);
				}}
				onOpenChange={(next) => {
					if (!next) setPendingDelete(undefined);
				}}
				open={!!pendingDelete}
				title="Remove this account?"
			/>
		</div>
	);
}

/** Read-only health of the daemon this dashboard is talking to. */
function DaemonSection() {
	const { me, status, loading, error, refresh } = useStore();
	const [refreshing, setRefreshing] = useState(false);

	async function runRefresh() {
		setRefreshing(true);
		try {
			await refresh();
		} finally {
			setRefreshing(false);
		}
	}

	return (
		<div className="max-w-2xl space-y-6">
			<SectionHead
				action={
					<Button
						aria-busy={refreshing}
						disabled={refreshing}
						onClick={() => void runRefresh()}
						size="sm"
						variant="outline"
					>
						{refreshing ? <Spinner /> : <ArrowsClockwise />}
						Refresh
					</Button>
				}
				badge={
					error ? (
						<Badge variant="destructive">unreachable</Badge>
					) : status ? (
						<Badge variant="secondary">
							<StatusIndicator color="emerald" pulse={false} />
							connected
						</Badge>
					) : (
						<Badge variant="outline">{loading ? "connecting…" : "idle"}</Badge>
					)
				}
				desc="What this dashboard is talking to."
				title="Daemon"
			/>
			<Card className="shadow-none dark:ring-0">
				<CardContent className="p-0">
					<Row label="Version" value={status?.version ?? "—"} />
					<Row label="Backend" value={status?.backend ?? "—"} />
					<Row label="Listen address" value={status?.addr ?? "—"} />
					<Row label="Uptime" value={status?.up ?? "—"} />
					<Row
						label="Storage"
						value={
							status
								? `${status.volumes_backed} · ${status.volumes} volume${status.volumes === 1 ? "" : "s"}`
								: "—"
						}
					/>
					<Row
						label="Warm pool"
						value={
							status
								? `${status.pool.idle} idle · ${status.pool.booting} booting · ${status.pool.size} slot${status.pool.size === 1 ? "" : "s"}`
								: "—"
						}
					/>
					<Row label="Default image" value={status?.default_image ?? "—"} />
					<Row
						label="Signed in"
						value={me ? `${me.user.email} · ${me.role}` : "—"}
					/>
				</CardContent>
			</Card>
		</div>
	);
}

/**
 * Settings as four tabs — account, team, machine credentials, daemon health.
 * Each tab lays its heading straight on the page (setup's split-screen
 * language); tables and row lists sit in a card below it, and every create
 * flow (invite, token) opens in a modal. The Team tab only exists for
 * admins; everyone else simply never sees it.
 */
export function SettingsPage() {
	const { me } = useStore();
	const canManage = roleRank(me?.role) >= roleRank("admin");

	return (
		<Tabs className="gap-6" defaultValue="account">
			<TabsList>
				<TabsTrigger value="account">Account</TabsTrigger>
				{canManage && <TabsTrigger value="team">Team</TabsTrigger>}
				<TabsTrigger value="tokens">API tokens</TabsTrigger>
				<TabsTrigger value="daemon">Daemon</TabsTrigger>
			</TabsList>

			<TabsContent value="account">
				<AccountSection />
			</TabsContent>

			{canManage && (
				<TabsContent value="team">
					<TeamSection />
				</TabsContent>
			)}

			<TabsContent value="tokens">
				<TokensSection />
			</TabsContent>

			<TabsContent value="daemon">
				<DaemonSection />
			</TabsContent>
		</Tabs>
	);
}
