import { useState } from "react";
import { Button } from "@/components/ui/button";
import {
	Dialog,
	DialogContent,
	DialogDescription,
	DialogFooter,
	DialogHeader,
	DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Field } from "@/modules/desktops/field";
import { Spinner } from "@/components/spinner";
import { useStore } from "@/lib/stores";
import { api } from "@/lib/api";
import { parseList } from "@/modules/desktops/desktops.data";
import { usePending } from "@/lib/use-pending";

type Props = {
	/** The desktop being edited. The parent renders this only while set. */
	desktopId: string;
	initialAllow: string;
	initialDeny: string;
	onClose: () => void;
	/** Failures surface in the page's notice, above the list — as they always have. */
	onError: (message: string | undefined) => void;
};

/**
 * Egress policy for one desktop: which hostnames it may reach. Mounted only
 * while a desktop is selected, so the fields open prefilled from that desktop.
 */
export function EgressPolicyDialog({
	desktopId,
	initialAllow,
	initialDeny,
	onClose,
	onError,
}: Props) {
	const { refresh } = useStore();
	const [allow, setAllow] = useState(initialAllow);
	const [deny, setDeny] = useState(initialDeny);
	const { pending: busy, run } = usePending();

	async function savePolicy() {
		onError(undefined);
		await run(async () => {
			try {
				await api.desktops.setPolicy(
					desktopId,
					parseList(allow),
					parseList(deny),
				);
				await refresh();
				onClose();
			} catch (e) {
				onError(e instanceof Error ? e.message : "policy update failed");
			}
		});
	}

	return (
		<Dialog
			onOpenChange={(next) => {
				if (!next) onClose();
			}}
			open
		>
			<DialogContent>
				<DialogHeader>
					<DialogTitle>Egress policy</DialogTitle>
					<DialogDescription>
						Domains <span className="font-mono">{desktopId}</span> may reach.
						An empty allow list means allow-all; a non-empty one means
						default-deny. Deny always wins. Comma-separated.
					</DialogDescription>
				</DialogHeader>
				<div className="grid gap-4 py-2">
					<Field id="policy-allow" label="Allow">
						<Input
							id="policy-allow"
							onChange={(e) => setAllow(e.target.value)}
							placeholder="api.openai.com, pypi.org"
							value={allow}
						/>
					</Field>
					<Field id="policy-deny" label="Deny">
						<Input
							id="policy-deny"
							onChange={(e) => setDeny(e.target.value)}
							placeholder="ads.example.com"
							value={deny}
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
	);
}
