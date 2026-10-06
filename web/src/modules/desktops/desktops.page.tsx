import { useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { SectionHead } from "@/components/section-head";
import { useStore } from "@/lib/stores";
import { api } from "@/lib/api";
import { joinList } from "@/modules/desktops/desktops.data";
import { DesktopTable } from "@/modules/desktops/desktop-table";
import { NewDesktopDialog } from "@/modules/desktops/create-desktop-dialog";
import { EgressPolicyDialog } from "@/modules/desktops/egress-policy-dialog";
import { CheckpointsDialog } from "@/modules/desktops/checkpoints-dialog";
import { Plus as PlusIcon } from "@phosphor-icons/react";

/**
 * Desktops: one list of MicroVMs, with the three destructive or slow flows
 * (create, policy edit, destroy) kept in their own components so this file
 * stays a matter of wiring.
 */
export function DesktopsPage() {
	const { desktops, refresh } = useStore();
	const [creating, setCreating] = useState(false);
	const [policyFor, setPolicyFor] = useState<string>();
	const [policyInitial, setPolicyInitial] = useState({ allow: "", deny: "" });
	const [pendingDestroy, setPendingDestroy] = useState<string>();
	const [checkpointsFor, setCheckpointsFor] = useState<string>();
	const [error, setError] = useState<string>();

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

	async function hibernate(id: string) {
		setError(undefined);
		try {
			await api.desktops.hibernate(id);
			await refresh();
		} catch (e) {
			setError(e instanceof Error ? e.message : "hibernate failed");
		}
	}

	async function wake(id: string) {
		setError(undefined);
		try {
			await api.desktops.wake(id);
			await refresh();
		} catch (e) {
			setError(e instanceof Error ? e.message : "wake failed");
		}
	}

	async function checkpoint(id: string) {
		setError(undefined);
		try {
			await api.desktops.checkpoint(id);
			await refresh();
		} catch (e) {
			setError(e instanceof Error ? e.message : "checkpoint failed");
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
				desc="MicroVMs managed by this daemon. Pause freezes one in place; Hibernate checkpoints it to disk and frees its RAM; Destroy frees it."
				title="Desktops"
			/>
			<DesktopTable
				desktops={desktops}
				error={error}
				onDestroy={setPendingDestroy}
				onOpen={open}
				onPause={(id) => void pause(id)}
				onResume={(id) => void resume(id)}
				onHibernate={(id) => void hibernate(id)}
				onWake={(id) => void wake(id)}
				onCheckpoint={(id) => void checkpoint(id)}
				onCheckpoints={(id) => setCheckpointsFor(id)}
				onPolicy={(d) => {
					setPolicyFor(d.id);
					setPolicyInitial({ allow: joinList(d.allow), deny: joinList(d.deny) });
				}}
			/>
			<NewDesktopDialog onClose={setCreating} open={creating} />
			{policyFor ? (
				<EgressPolicyDialog
					desktopId={policyFor}
					initialAllow={policyInitial.allow}
					initialDeny={policyInitial.deny}
					onClose={() => setPolicyFor(undefined)}
					onError={setError}
				/>
			) : null}
			{checkpointsFor ? (
				<CheckpointsDialog
					desktopId={checkpointsFor}
					onClose={() => setCheckpointsFor(undefined)}
					onError={setError}
				/>
			) : null}
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
