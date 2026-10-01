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
import { Field } from "@/components/field";
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
	const { desktops, images, imageMeta, status, volumes, refresh } = useStore();
	const [creating, setCreating] = useState(false);
	const [image, setImage] = useState<string>("");
	const [allow, setAllow] = useState<string>("");
	const [deny, setDeny] = useState<string>("");
	const [createError, setCreateError] = useState<string>();
	const [cpus, setCpus] = useState("");
	const [memMiB, setMemMiB] = useState("");
	const [volume, setVolume] = useState("");
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
					...(volume ? { volume } : {}),
					...(Number(cpus) > 0 ? { cpus: Number(cpus) } : {}),
					...(Number(memMiB) > 0 ? { mem_mib: Number(memMiB) } : {}),
					allow: parseList(allow),
					deny: parseList(deny),
				});
				setCreating(false);
				setImage("");
				setAllow("");
				setDeny("");
				setCpus("");
				setMemMiB("");
				setVolume("");
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
											<span className="flex items-center gap-2">
												<StateBadge state={d.state} />
												{d.headless ? (
													<Badge variant="outline">Headless</Badge>
												) : null}
											</span>
										</TableCell>
										<TableCell className="pr-6 text-right">
											<RowActions label={`Actions for ${d.id}`}>
												{d.headless ? (
													<DropdownMenuItem disabled>
														<SquareArrowOutUpRightIcon />
														No screen (headless)
													</DropdownMenuItem>
												) : (
													<DropdownMenuItem
														disabled={
															d.state === "dead" || d.state === "paused"
														}
														onSelect={() => open(d.id)}
													>
														<SquareArrowOutUpRightIcon />
														Open console
													</DropdownMenuItem>
												)}
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
						setCpus("");
						setMemMiB("");
						setVolume("");
					}
				}}
				open={creating}
			>
				<DialogContent>
					<DialogHeader>
						<DialogTitle>New desktop</DialogTitle>
						<DialogDescription>
							Boot a microVM from a guest image.
						</DialogDescription>
					</DialogHeader>
					<form
						className="space-y-4"
						onSubmit={(e) => {
							e.preventDefault();
							void create();
						}}
					>
						<Field id="new-desktop-image" label="Image">
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
											{imageMeta[name]?.headless ? " · headless" : ""}
										</SelectItem>
									))}
								</SelectContent>
							</Select>
						</Field>
						<div className="grid gap-4 sm:grid-cols-2 sm:gap-3">
							<Field id="new-desktop-cpus" label="vCPUs">
								<Input
									disabled={busy}
									id="new-desktop-cpus"
									inputMode="numeric"
									min={1}
									onChange={(e) => setCpus(e.target.value)}
									placeholder={
										status?.defaults?.cpus ? `${status.defaults.cpus}` : ""
									}
									type="number"
									value={cpus}
								/>
							</Field>
							<Field id="new-desktop-mem" label="Memory (MiB)">
								<Input
									disabled={busy}
									id="new-desktop-mem"
									inputMode="numeric"
									min={256}
									onChange={(e) => setMemMiB(e.target.value)}
									placeholder={
										status?.defaults?.mem_mib ? `${status.defaults.mem_mib}` : ""
									}
									type="number"
									value={memMiB}
								/>
							</Field>
						</div>
						<Field
							hint="Keeps your files across destroys."
							id="new-desktop-volume"
							label="Volume"
						>
							<Select
								disabled={busy}
								onValueChange={setVolume}
								value={volume || "none"}
							>
								<SelectTrigger
									aria-label="Volume to attach"
									className="w-full"
									id="new-desktop-volume"
									size="sm"
								>
									<SelectValue />
								</SelectTrigger>
								<SelectContent>
									<SelectItem value="none">None — ephemeral</SelectItem>
									{volumes.map((v) => (
										<SelectItem key={v.name} value={v.name}>
											{v.name}
										</SelectItem>
									))}
								</SelectContent>
							</Select>
						</Field>
						<Field
							hint="Empty allows everything."
							id="new-desktop-allow"
							label="Allow"
						>
							<Input
								disabled={busy}
								id="new-desktop-allow"
								onChange={(e) => setAllow(e.target.value)}
								placeholder="api.openai.com, pypi.org"
								value={allow}
							/>
						</Field>
						<Field id="new-desktop-deny" label="Deny">
							<Input
								disabled={busy}
								id="new-desktop-deny"
								onChange={(e) => setDeny(e.target.value)}
								placeholder="ads.example.com"
								value={deny}
							/>
						</Field>
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
					<div className="grid gap-4 py-2">
						<Field id="policy-allow" label="Allow">
							<Input
								id="policy-allow"
								onChange={(e) => setPolicyAllow(e.target.value)}
								placeholder="api.openai.com, pypi.org"
								value={policyAllow}
							/>
						</Field>
						<Field id="policy-deny" label="Deny">
							<Input
								id="policy-deny"
								onChange={(e) => setPolicyDeny(e.target.value)}
								placeholder="ads.example.com"
								value={policyDeny}
							/>
						</Field>
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
