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
import {
	Select,
	SelectContent,
	SelectItem,
	SelectTrigger,
	SelectValue,
} from "@/components/ui/select";
import { Field } from "@/modules/desktops/field";
import { NoticeLine } from "@/components/notice-line";
import { Spinner } from "@/components/spinner";
import { useStore } from "@/lib/stores";
import { api } from "@/lib/api";
import { parseList } from "@/modules/desktops/desktops.data";
import { usePending } from "@/lib/use-pending";
import { Plus as PlusIcon } from "@phosphor-icons/react";

type Props = {
	open: boolean;
	/** Closes the dialog; also called by the parent when it is dismissed. */
	onClose: (next: boolean) => void;
};

/**
 * Boot a microVM from a guest image. Owns its own form state so a cancelled or
 * completed create always comes back to a clean form, exactly as before.
 */
export function NewDesktopDialog({ open, onClose }: Props) {
	const { images, imageMeta, status, volumes, refresh } = useStore();
	const [image, setImage] = useState<string>("");
	const [allow, setAllow] = useState<string>("");
	const [deny, setDeny] = useState<string>("");
	const [createError, setCreateError] = useState<string>();
	const [cpus, setCpus] = useState("");
	const [memMiB, setMemMiB] = useState("");
	const [volume, setVolume] = useState("");
	const { pending: busy, run } = usePending();

	const selected = image || status?.default_image || images[0] || "";
	const canCreate = !!selected && !busy;

	function reset() {
		setCreateError(undefined);
		setImage("");
		setAllow("");
		setDeny("");
		setCpus("");
		setMemMiB("");
		setVolume("");
	}

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
				onClose(false);
				reset();
				await refresh();
			} catch (e) {
				setCreateError(e instanceof Error ? e.message : "create failed");
			}
		});
	}

	return (
		<Dialog
			onOpenChange={(next) => {
				if (busy) return;
				onClose(next);
				if (!next) reset();
			}}
			open={open}
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
							onClick={() => onClose(false)}
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
	);
}
