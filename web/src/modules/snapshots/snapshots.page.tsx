import { useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent } from "@/components/ui/card";
import { DropdownMenuItem } from "@/components/ui/dropdown-menu";
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
import { EmptyState } from "@/components/empty-state";
import { useStore } from "@/lib/stores";
import { api } from "@/lib/api";
import { bytes, since } from "@/lib/format";
import { Trash as TrashIcon } from "@phosphor-icons/react";

/** Read-only list: snapshots are taken from a volume's row menu. */
export function SnapshotsPage() {
	const { snapshots, refresh } = useStore();
	const [error, setError] = useState<string>();
	const [pendingDelete, setPendingDelete] = useState<string>();

	async function remove(id: string) {
		setError(undefined);
		try {
			await api.snapshots.remove(id);
			await refresh();
		} catch (e) {
			setError(e instanceof Error ? e.message : "delete failed");
		}
	}

	const total = snapshots.reduce((sum, s) => sum + (s.size || 0), 0);

	return (
		<div className="space-y-6">
			<SectionHead
				badge={<Badge variant="secondary">{snapshots.length}</Badge>}
				desc={`${bytes(total)} stored. Take one from a volume's row menu on the Volumes page.`}
				title="Snapshots"
			/>
			<Card className="shadow-none dark:ring-0">
				<CardContent className="p-0">
					<NoticeLine message={error} />
					{snapshots.length === 0 ? (
						<EmptyState
							hint="Open Volumes and pick Take snapshot from a volume's menu."
							title="No snapshots"
						/>
					) : (
						<Table>
							<TableHeader>
								<TableRow className="hover:bg-transparent">
									<TableHead className="pl-6">Snapshot</TableHead>
									<TableHead>Volume</TableHead>
									<TableHead className="text-right">Size</TableHead>
									<TableHead className="hidden md:table-cell">Chunk</TableHead>
									<TableHead className="hidden sm:table-cell">
										Created
									</TableHead>
									<TableHead className="pr-6 text-right">Actions</TableHead>
								</TableRow>
							</TableHeader>
							<TableBody>
								{snapshots.map((s) => (
									<TableRow className="h-14 hover:bg-transparent" key={s.id}>
										<TableCell className="max-w-44 truncate pl-6 font-mono font-medium text-sm">
											{s.id}
										</TableCell>
										<TableCell className="text-muted-foreground text-sm">
											{s.volume}
										</TableCell>
										<TableCell className="text-right text-muted-foreground text-sm tabular-nums">
											{bytes(s.size)}
										</TableCell>
										<TableCell className="hidden text-muted-foreground text-sm md:table-cell tabular-nums">
											{bytes(s.chunk_size)}
										</TableCell>
										<TableCell className="hidden text-muted-foreground text-sm sm:table-cell">
											{since(s.created_at)}
										</TableCell>
										<TableCell className="pr-6 text-right">
											<RowActions label={`Actions for ${s.id}`}>
												<DropdownMenuItem
													onSelect={() => setPendingDelete(s.id)}
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
			<ConfirmDialog
				description={
					<>
						<span className="font-mono">{pendingDelete}</span> is erased. The
						volume it was taken from is untouched.
					</>
				}
				onConfirm={async () => {
					if (pendingDelete) await remove(pendingDelete);
				}}
				onOpenChange={(next) => {
					if (!next) setPendingDelete(undefined);
				}}
				open={!!pendingDelete}
				title="Delete this snapshot?"
			/>
		</div>
	);
}
