import { useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { DropdownMenuItem } from "@/components/ui/dropdown-menu";
import {
	Select,
	SelectContent,
	SelectItem,
	SelectTrigger,
	SelectValue,
} from "@/components/ui/select";
import {
	Dialog,
	DialogContent,
	DialogDescription,
	DialogFooter,
	DialogHeader,
	DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import {
	Table,
	TableBody,
	TableCell,
	TableHead,
	TableHeader,
	TableRow,
} from "@/components/ui/table";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { RowActions } from "@/components/row-actions";
import { SectionHead } from "@/components/section-head";
import { Spinner } from "@/components/spinner";
import { StateBadge } from "@/components/state-badge";
import { EmptyState } from "@/components/empty-state";
import { NoticeLine } from "@/components/notice-line";
import { useStore } from "@/lib/store";
import { api } from "@/lib/api";
import { uptime } from "@/lib/format";
import { usePending } from "@/lib/use-pending";
import {
	Plus as PlusIcon,
	ArrowSquareOut as SquareArrowOutUpRightIcon,
	Pause as PauseIcon,
	Play as PlayIcon,
	Trash as TrashIcon,
	Globe as GlobeIcon,
} from "@phosphor-icons/react";

function parseList(s: string): string[] {
	return s
		.split(",")
		.map((x) => x.trim())
		.filter(Boolean);
}
function joinList(a?: string[]): string {
	return (a ?? []).join(", ");
}

export function DesktopsPage() {
	const { desktops, images, status, refresh } = useStore();
	const [creating, setCreating] = useState(false);
	const [image, setImage] = useState<string>("");
	const [allow, setAllow] = useState<string>("");
	const [deny, setDeny] = useState<string>("");
	const [createError, setCreateError] = useState<string>();
	const [error, setError] = useState<string>();
	const [pendingDestroy, setPendingDestroy] = useState<string>();
	const [policyFor, setPolicyFor] = useState<string>();
	const [policyAllow, setPolicyAllow] = useState<string>("");
	const [policyDeny, setPolicyDeny] = useState<string>("");
	const { pending: busy, run } = usePending();

	const selected = image || status?.default_image || images[0] || "";
	const canCreate = !!selected && !busy;

	async function create() {
		if (!canCreate) return;
		setCreateError(undefined);
		await run(async () => {
			try {
				await api.desktops.create({
					image: selected,
					allow: parseList(allow),
					deny: parseList(deny),
				});
				setCreating(false);
				setImage("");
				setAllow("");
				setDeny("");
				await refresh();
			} catch (e) {
				setCreateError(e instanceof Error ? e.message : "create failed");
			}
		});
	}

	async function savePolicy() {
		if (!policyFor) return;
		setError(undefined);
		await run(async () => {
			try {
				await api.desktops.setPolicy(
					policyFor,
					parseList(policyAllow),
					parseList(policyDeny),
				);
				await refresh();
				setPolicyFor(undefined);
			} catch (e) {
				setError(e instanceof Error ? e.message : "policy update failed");
			}
		});
	}

	async function destroy(id: string) {
		setError(undefined);
		try {
			await api.desktops.destroy(id);
			await refresh();
		} catch (e) {
			setError(e instanceof Error ? e.message : "destroy failed");
		}
	}

	async function pause(id: string) {
		setError(undefined);
		try {
			await api.desktops.pause(id);
			await refresh();
		} catch (e) {
			setError(e instanceof Error ? e.message : "pause failed");
		}
	}

	async function resume(id: string) {
		setError(undefined);
		try {
			await api.desktops.resume(id);
			await refresh();
		} catch (e) {
			setError(e instanceof Error ? e.message : "resume failed");
		}
	}

	function open(id: string) {
		window.open(`/d/${id}`, "_blank", "noopener");
	}

	return (
		<div className="space-y-6">
			<SectionHead
				action={
					<Button onClick={() => setCreating(true)} size="sm">
						<PlusIcon />
						New desktop
					</Button>
				}
				badge={<Badge variant="secondary">{desktops.length}</Badge>}
				desc="MicroVMs managed by this daemon. Creating one boots it into the pool. Pause keeps a desktop's memory; Destroy frees it."
				title="Desktops"
			/>
			<Card className="shadow-none dark:ring-0">
				<CardContent className="p-0">
					<NoticeLine message={error} />
					{desktops.length === 0 ? (
						<EmptyState
							hint="Pick an image in New desktop and boot the first one."
							title="No desktops running"
						/>
					) : (
						<Table>
							<TableHeader>
								<TableRow className="hover:bg-transparent">
									<TableHead className="pl-6">Desktop</TableHead>
									<TableHead className="hidden sm:table-cell">
										Guest IP
									</TableHead>
									<TableHead className="hidden md:table-cell">
										Uptime
									</TableHead>
									<TableHead className="hidden lg:table-cell">
										Volume
									</TableHead>
									<TableHead>State</TableHead>
									<TableHead className="pr-6 text-right">Actions</TableHead>
								</TableRow>
							</TableHeader>
							<TableBody>
								{desktops.map((d) => (
									<TableRow className="h-14 hover:bg-transparent" key={d.id}>
										<TableCell className="max-w-40 truncate pl-6 font-mono font-medium text-sm">
											{d.id}
										</TableCell>
										<TableCell className="hidden text-muted-foreground text-sm sm:table-cell">
											<span className="tabular-nums">{d.guest_ip ?? "—"}</span>
										</TableCell>
										<TableCell className="hidden text-muted-foreground text-sm md:table-cell">
											<span className="tabular-nums">{uptime(d.started)}</span>
										</TableCell>
										<TableCell className="hidden max-w-32 text-muted-foreground text-sm lg:table-cell">
											<span className="line-clamp-1">{d.volume ?? "—"}</span>
										</TableCell>
										<TableCell>
											<StateBadge state={d.state} />
										</TableCell>
										<TableCell className="pr-6 text-right">
											<RowActions label={`Actions for ${d.id}`}>
												<DropdownMenuItem
													disabled={d.state === "dead" || d.state === "paused"}
													onSelect={() => open(d.id)}
												>
													<SquareArrowOutUpRightIcon />
													Open console
												</DropdownMenuItem>
												{d.state === "paused" ? (
													<DropdownMenuItem onSelect={() => void resume(d.id)}>
														<PlayIcon />
														Resume
													</DropdownMenuItem>
												) : (
													<DropdownMenuItem
														disabled={
															d.state === "booting" || d.state === "dead"
														}
														onSelect={() => void pause(d.id)}
													>
														<PauseIcon />
														Pause
													</DropdownMenuItem>
												)}
												<DropdownMenuItem
													onSelect={() => {
														setPolicyFor(d.id);
														setPolicyAllow(joinList(d.allow));
														setPolicyDeny(joinList(d.deny));
													}}
												>
													<GlobeIcon />
													Egress policy…
												</DropdownMenuItem>
												<DropdownMenuItem
													onSelect={() => setPendingDestroy(d.id)}
													variant="destructive"
												>
													<TrashIcon />
													Destroy
												</DropdownMenuItem>
											</RowActions>
										</TableCell>
									</TableRow>
								))}
							</TableBody>
						</Table>
					)}
				</CardContent>
			</Card>
			<Dialog
				onOpenChange={(next) => {
					if (busy) return;
					setCreating(next);
					if (!next) {
						setCreateError(undefined);
						setImage("");
						setAllow("");
						setDeny("");
					}
				}}
				open={creating}
			>
				<DialogContent>
					<DialogHeader>
						<DialogTitle>New desktop</DialogTitle>
						<DialogDescription>
							Boot a microVM from a guest image. Egress lists are
							comma-separated; an empty allow list means allow-all, a non-empty
							one means default-deny. Deny always wins.
						</DialogDescription>
					</DialogHeader>
					<form
						className="space-y-4"
						onSubmit={(e) => {
							e.preventDefault();
							void create();
						}}
					>
						<div className="space-y-2">
							<label className="text-sm" htmlFor="new-desktop-image">
								Image
							</label>
							<Select disabled={busy} onValueChange={setImage} value={selected}>
								<SelectTrigger
									aria-label="Image for the new desktop"
									className="w-full"
									id="new-desktop-image"
									size="sm"
								>
									<SelectValue placeholder="Image" />
								</SelectTrigger>
								<SelectContent>
									{images.map((name) => (
										<SelectItem key={name} value={name}>
											{name}
											{name === status?.default_image ? " · default" : ""}
										</SelectItem>
									))}
								</SelectContent>
							</Select>
						</div>
						<div className="space-y-2">
							<label className="text-sm" htmlFor="new-desktop-allow">
								Allow
							</label>
							<Input
								disabled={busy}
								id="new-desktop-allow"
								onChange={(e) => setAllow(e.target.value)}
								placeholder="api.openai.com, pypi.org"
								value={allow}
							/>
							<p className="text-muted-foreground text-xs">
								Optional — empty allows every domain; a list means
								default-deny.
							</p>
						</div>
						<div className="space-y-2">
							<label className="text-sm" htmlFor="new-desktop-deny">
								Deny
							</label>
							<Input
								disabled={busy}
								id="new-desktop-deny"
								onChange={(e) => setDeny(e.target.value)}
								placeholder="ads.example.com"
								value={deny}
							/>
						</div>
						<NoticeLine
							className="border-b-0 px-0 py-0"
							message={createError}
						/>
						<DialogFooter>
							<Button
								disabled={busy}
								onClick={() => setCreating(false)}
								size="sm"
								type="button"
								variant="outline"
							>
								Cancel
							</Button>
							<Button aria-busy={busy} disabled={!canCreate} size="sm" type="submit">
								{busy ? (
									<>
										<Spinner />
										Booting…
									</>
								) : (
									<>
										<PlusIcon />
										Create desktop
									</>
								)}
							</Button>
						</DialogFooter>
					</form>
				</DialogContent>
			</Dialog>
			<Dialog
				onOpenChange={(next) => {
					if (!next) setPolicyFor(undefined);
				}}
				open={!!policyFor}
			>
				<DialogContent>
					<DialogHeader>
						<DialogTitle>Egress policy</DialogTitle>
						<DialogDescription>
							Domains <span className="font-mono">{policyFor}</span> may reach.
							An empty allow list means allow-all; a non-empty one means
							default-deny. Deny always wins. Comma-separated.
						</DialogDescription>
					</DialogHeader>
					<div className="grid gap-3 py-2">
						<label className="grid gap-1 text-sm">
							<span className="text-muted-foreground">Allow</span>
							<Input
								onChange={(e) => setPolicyAllow(e.target.value)}
								placeholder="api.openai.com, pypi.org"
								value={policyAllow}
							/>
						</label>
						<label className="grid gap-1 text-sm">
							<span className="text-muted-foreground">Deny</span>
							<Input
								onChange={(e) => setPolicyDeny(e.target.value)}
								placeholder="ads.example.com"
								value={policyDeny}
							/>
						</label>
					</div>
					<DialogFooter>
						<Button
							aria-busy={busy}
							disabled={busy}
							onClick={() => void savePolicy()}
							size="sm"
						>
							{busy ? <Spinner /> : null}
							Save policy
						</Button>
					</DialogFooter>
				</DialogContent>
			</Dialog>
			<ConfirmDialog
				confirmLabel="Destroy"
				description={
					<>
						<span className="font-mono">{pendingDestroy}</span> stops immediately
						and its guest disk is discarded. Volumes it uses are kept.
					</>
				}
				onConfirm={async () => {
					if (pendingDestroy) await destroy(pendingDestroy);
				}}
				onOpenChange={(next) => {
					if (!next) setPendingDestroy(undefined);
				}}
				open={!!pendingDestroy}
				title="Destroy this desktop?"
			/>
		</div>
	);
}
