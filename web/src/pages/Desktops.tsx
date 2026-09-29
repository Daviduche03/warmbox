import { useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
	Card,
	CardContent,
	CardDescription,
	CardHeader,
	CardTitle,
} from "@/components/ui/card";
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
import { Spinner } from "@/components/spinner";
import { StateBadge } from "@/components/state-badge";
import { EmptyState } from "@/components/empty-state";
import { NoticeLine } from "@/components/notice-line";
import { useStore } from "@/lib/store";
import { api, getToken } from "@/lib/api";
import { uptime } from "@/lib/format";
import { usePending } from "@/lib/use-pending";
import {
	Plus as PlusIcon,
	ArrowSquareOut as SquareArrowOutUpRightIcon,
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
	const [image, setImage] = useState<string>("");
	const [allow, setAllow] = useState<string>("");
	const [deny, setDeny] = useState<string>("");
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
		setError(undefined);
		await run(async () => {
			try {
				await api.desktops.create({
					image: selected,
					allow: parseList(allow),
					deny: parseList(deny),
				});
				await refresh();
			} catch (e) {
				setError(e instanceof Error ? e.message : "create failed");
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

	function open(id: string) {
		const token = getToken();
		const qs = token ? `?token=${encodeURIComponent(token)}` : "";
		window.open(`/d/${id}${qs}`, "_blank", "noopener");
	}

	return (
		<Card className="shadow-none dark:ring-0">
			<CardHeader className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
				<div className="min-w-0 space-y-2">
					<div className="flex flex-wrap items-center gap-2">
						<CardTitle>Desktops</CardTitle>
						<Badge variant="secondary">{desktops.length}</Badge>
					</div>
					<CardDescription>
						MicroVMs managed by this daemon. Creating one boots it into the pool.
					</CardDescription>
				</div>
				<div className="flex w-full flex-col gap-2 sm:w-auto sm:flex-row sm:items-center">
					<Select disabled={busy} onValueChange={setImage} value={selected}>
						<SelectTrigger
							aria-label="Image for the new desktop"
							className="w-full sm:w-40"
							size="sm"
						>
							<SelectValue placeholder="Image" />
						</SelectTrigger>
						<SelectContent align="end">
							{images.map((name) => (
								<SelectItem key={name} value={name}>
									{name}
									{name === status?.default_image ? " · default" : ""}
								</SelectItem>
							))}
						</SelectContent>
					</Select>
					<Input
						aria-label="Allowed domains for the new desktop"
						className="w-full sm:w-44"
						disabled={busy}
						onChange={(e) => setAllow(e.target.value)}
						placeholder="allow: api.openai.com,…"
						value={allow}
					/>
					<Input
						aria-label="Denied domains for the new desktop"
						className="w-full sm:w-40"
						disabled={busy}
						onChange={(e) => setDeny(e.target.value)}
						placeholder="deny: …"
						value={deny}
					/>
					<Button
						aria-busy={busy}
						disabled={!canCreate}
						onClick={() => void create()}
						size="sm"
					>
						{busy ? (
							<>
								<Spinner />
								Booting…
							</>
						) : (
							<>
								<PlusIcon />
								New desktop
							</>
						)}
					</Button>
				</div>
			</CardHeader>
			<CardContent className="p-0">
				<NoticeLine message={error} />
				{desktops.length === 0 ? (
					<EmptyState
						hint="Pick an image above and boot the first one."
						title="No desktops running"
					/>
				) : (
					<Table>
						<TableHeader>
							<TableRow className="hover:bg-transparent">
								<TableHead className="pl-6">Desktop</TableHead>
								<TableHead className="hidden sm:table-cell">Guest IP</TableHead>
								<TableHead className="hidden md:table-cell">Uptime</TableHead>
								<TableHead className="hidden lg:table-cell">Volume</TableHead>
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
												disabled={d.state === "dead"}
												onSelect={() => open(d.id)}
											>
												<SquareArrowOutUpRightIcon />
												Open console
											</DropdownMenuItem>
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
		</Card>
	);
}
