import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import {
	Dialog,
	DialogContent,
	DialogDescription,
	DialogHeader,
	DialogTitle,
} from "@/components/ui/dialog";
import { Spinner } from "@/components/spinner";
import { useStore } from "@/lib/stores";
import { api } from "@/lib/api";
import { bytes, since } from "@/lib/format";
import { usePending } from "@/lib/use-pending";

type Checkpoint = {
	name: string;
	size: number;
	created: string;
	image: string;
};

type Props = {
	/** The desktop whose checkpoints are listed. The parent renders this only while set. */
	desktopId: string;
	onClose: () => void;
	/** Failures surface in the page's notice, above the list — as they always have. */
	onError: (message: string | undefined) => void;
};

/**
 * Checkpoints for one desktop: restore rolls the running VM back in place
 * (same id, guest uptime continues), delete drops the snapshot file.
 * Mounted only while a desktop is selected.
 */
export function CheckpointsDialog({ desktopId, onClose, onError }: Props) {
	const { refresh } = useStore();
	const [items, setItems] = useState<Checkpoint[]>();
	const { pending: busy, run } = usePending();

	useEffect(() => {
		let live = true;
		api.desktops
			.checkpoints(desktopId)
			.then((out) => {
				if (live) setItems(out.checkpoints);
			})
			.catch((e: unknown) => {
				onError(e instanceof Error ? e.message : "checkpoints failed");
				onClose();
			});
		return () => {
			live = false;
		};
		// Loading once per desktop; errors close the dialog via onError/onClose.
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [desktopId]);

	async function remove(name: string) {
		onError(undefined);
		await run(async () => {
			try {
				await api.desktops.checkpointDelete(desktopId, name);
				setItems((prev) => prev?.filter((c) => c.name !== name));
			} catch (e) {
				onError(e instanceof Error ? e.message : "delete failed");
			}
		});
	}

	async function restore(name: string) {
		onError(undefined);
		await run(async () => {
			try {
				await api.desktops.restore(desktopId, name);
				await refresh();
				onClose();
			} catch (e) {
				onError(e instanceof Error ? e.message : "restore failed");
			}
		});
	}

	return (
		<Dialog onOpenChange={(next) => !next && onClose()} open>
			<DialogContent>
				<DialogHeader>
					<DialogTitle>Checkpoints</DialogTitle>
					<DialogDescription>
						<span className="font-mono">{desktopId}</span> — restore rolls
						this desktop back without booting; the guest keeps its identity.
					</DialogDescription>
				</DialogHeader>
				{items === undefined ? (
					<div className="flex items-center gap-2 py-6 text-muted-foreground text-sm">
						<Spinner />
						Loading checkpoints…
					</div>
				) : items.length === 0 ? (
					<p className="py-6 text-muted-foreground text-sm">
						No checkpoints yet — take one from the row menu first.
					</p>
				) : (
					<ul className="space-y-2">
						{items.map((c) => (
							<li
								className="flex items-center gap-3 rounded-md border px-3 py-2"
								key={c.name}
							>
								<div className="min-w-0 flex-1">
									<div className="truncate font-mono text-sm">{c.name}</div>
									<div className="text-muted-foreground text-xs tabular-nums">
										{bytes(c.size)} · {since(c.created)}
										{c.image ? ` · ${c.image}` : null}
									</div>
								</div>
								<Button
									disabled={busy}
									onClick={() => void restore(c.name)}
									size="sm"
									variant="outline"
								>
									Restore
								</Button>
								<Button
									disabled={busy}
									onClick={() => void remove(c.name)}
									size="sm"
									variant="ghost"
								>
									Delete
								</Button>
							</li>
						))}
					</ul>
				)}
			</DialogContent>
		</Dialog>
	);
}
