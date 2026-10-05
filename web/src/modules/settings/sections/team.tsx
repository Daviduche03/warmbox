import { useEffect, useState, type FormEvent } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { DropdownMenuItem } from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { NoticeLine } from "@/components/notice-line";
import { RowActions } from "@/components/row-actions";
import { SectionHead } from "@/components/section-head";
import { Spinner } from "@/components/spinner";
import { useStore } from "@/lib/stores";
import { api, roleRank, ApiError, type Member } from "@/lib/api";
import { usePending } from "@/lib/use-pending";
import { Plus as PlusIcon, Trash as TrashIcon } from "@phosphor-icons/react";

const MANAGEABLE_ROLES = ["viewer", "member", "admin", "owner"] as const;

export function TeamSection() {
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
						<div className="space-y-3">
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
						<div className="space-y-3">
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
						<div className="space-y-3">
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
						<div className="space-y-3">
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
