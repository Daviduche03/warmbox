import { useState } from "react";
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
	Table,
	TableBody,
	TableCell,
	TableHead,
	TableHeader,
	TableRow,
} from "@/components/ui/table";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { NoticeLine } from "@/components/notice-line";
import { RowActions } from "@/components/row-actions";
import { SectionHead } from "@/components/section-head";
import { Spinner } from "@/components/spinner";
import { EmptyState } from "@/components/empty-state";
import { useStore } from "@/lib/stores";
import { api } from "@/lib/api";
import { bytes, since } from "@/lib/format";
import { usePending } from "@/lib/use-pending";
import { Camera as CameraIcon, Plus as PlusIcon, Trash as TrashIcon } from "@phosphor-icons/react";

export function VolumesPage() {
	const { volumes, refresh } = useStore();
	const [open, setOpen] = useState(false);
	const [name, setName] = useState("");
	const [size, setSize] = useState("");
	const [error, setError] = useState<string>();
	const [notice, setNotice] = useState<string>();
	const [pendingDelete, setPendingDelete] = useState<string>();
	const { pending: creating, run } = usePending();

	const canCreate = name.trim().length > 0 && !creating;

	function reset() {
		setName("");
		setSize("");
		setError(undefined);
	}

	async function create() {
		if (!canCreate) return;
		setError(undefined);
		await run(async () => {
			try {
				await api.volumes.create({
					name: name.trim(),
					...(size.trim() ? { size: size.trim() } : {}),
				});
				setOpen(false);
				reset();
				await refresh();
			} catch (e) {
				setError(e instanceof Error ? e.message : "create failed");
			}
		});
	}

	async function snapshot(volumeName: string) {
		setError(undefined);
		setNotice(undefined);
		await run(async () => {
			try {
				await api.snapshots.create(volumeName);
				setNotice(`Snapshot of ${volumeName} captured.`);
				await refresh();
			} catch (e) {
				setError(e instanceof Error ? e.message : "snapshot failed");
			}
		});
	}

	async function remove(volumeName: string) {
		setError(undefined);
		setNotice(undefined);
		try {
			await api.volumes.remove(volumeName);
			await refresh();
		} catch (e) {
			setError(e instanceof Error ? e.message : "delete failed");
		}
	}

	const total = volumes.reduce((sum, v) => sum + (v.size || 0), 0);

	return (
		<div className="space-y-6">
			<SectionHead
				action={
					<Button onClick={() => setOpen(true)} size="sm">
						<PlusIcon />
						New volume
					</Button>
				}
				badge={<Badge variant="secondary">{volumes.length}</Badge>}
				desc={`${bytes(total)} provisioned in total.`}
				title="Volumes"
			/>
			<Card className="shadow-none dark:ring-0">
				<CardContent className="p-0">
					<NoticeLine message={error} />
					<NoticeLine message={notice} tone="success" />
					{volumes.length === 0 ? (
						<EmptyState
							hint="Create one and the daemon allocates it."
							title="No volumes"
						/>
					) : (
						<Table>
							<TableHeader>
								<TableRow className="hover:bg-transparent">
									<TableHead className="pl-6">Name</TableHead>
									<TableHead className="text-right">Size</TableHead>
									<TableHead className="hidden md:table-cell">
										Chunk
									</TableHead>
									<TableHead className="hidden lg:table-cell">
										Source
									</TableHead>
									<TableHead className="hidden sm:table-cell">
										Updated
									</TableHead>
									<TableHead className="pr-6 text-right">Actions</TableHead>
								</TableRow>
							</TableHeader>
							<TableBody>
								{volumes.map((v) => (
									<TableRow className="h-14 hover:bg-transparent" key={v.name}>
										<TableCell className="max-w-52 truncate pl-6 font-medium">
											{v.name}
										</TableCell>
										<TableCell className="text-right text-muted-foreground text-sm tabular-nums">
											{bytes(v.size)}
										</TableCell>
										<TableCell className="hidden text-muted-foreground text-sm md:table-cell tabular-nums">
											{bytes(v.chunk_size)}
										</TableCell>
										<TableCell className="hidden text-muted-foreground text-sm lg:table-cell">
											{v.from ?? v.remote ?? "—"}
										</TableCell>
										<TableCell className="hidden text-muted-foreground text-sm sm:table-cell">
											{since(v.updated_at)}
										</TableCell>
										<TableCell className="pr-6 text-right">
											<RowActions label={`Actions for ${v.name}`}>
												<DropdownMenuItem onSelect={() => void snapshot(v.name)}>
													<CameraIcon />
													Take snapshot
												</DropdownMenuItem>
												<DropdownMenuItem
													onSelect={() => setPendingDelete(v.name)}
													variant="destructive"
												>
													<TrashIcon />
													Delete
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
					if (creating) return;
					setOpen(next);
					if (!next) reset();
				}}
				open={open}
			>
				<DialogContent>
					<DialogHeader>
						<DialogTitle>New volume</DialogTitle>
						<DialogDescription>
							A portable disk the daemon keeps locally. Attach it to a desktop
							when you create one.
						</DialogDescription>
					</DialogHeader>
					<form
						className="space-y-4"
						onSubmit={(e) => {
							e.preventDefault();
							void create();
						}}
					>
						<div className="space-y-3">
							<label className="text-sm" htmlFor="new-volume-name">
								Name
							</label>
							<Input
								autoFocus
								disabled={creating}
								id="new-volume-name"
								onChange={(e) => setName(e.target.value)}
								placeholder="mywork"
								value={name}
							/>
						</div>
						<div className="space-y-3">
							<label className="text-sm" htmlFor="new-volume-size">
								Size
							</label>
							<Input
								disabled={creating}
								id="new-volume-size"
								onChange={(e) => setSize(e.target.value)}
								placeholder="8G"
								value={size}
							/>
							<p className="text-muted-foreground text-xs">
								Optional — defaults to the daemon's volume size.
							</p>
						</div>
						<NoticeLine className="border-b-0 px-0 py-0" message={error} />
						<DialogFooter>
							<Button
								disabled={creating}
								onClick={() => setOpen(false)}
								type="button"
								variant="outline"
							>
								Cancel
							</Button>
							<Button aria-busy={creating} disabled={!canCreate} type="submit">
								{creating ? (
									<>
										<Spinner />
										Creating…
									</>
								) : (
									<>
										<PlusIcon />
										Create volume
									</>
								)}
							</Button>
						</DialogFooter>
					</form>
				</DialogContent>
			</Dialog>

			<ConfirmDialog
				description={
					<>
						<span className="font-mono">{pendingDelete}</span> and everything on
						it are erased. This cannot be undone.
					</>
				}
				onConfirm={async () => {
					if (pendingDelete) await remove(pendingDelete);
				}}
				onOpenChange={(next) => {
					if (!next) setPendingDelete(undefined);
				}}
				open={!!pendingDelete}
				title="Delete this volume?"
			/>
		</div>
	);
}
