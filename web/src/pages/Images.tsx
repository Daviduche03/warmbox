import { useState } from "react";
import { DownloadSimple as DownloadIcon } from "@phosphor-icons/react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import {
	Table,
	TableBody,
	TableCell,
	TableHead,
	TableHeader,
	TableRow,
} from "@/components/ui/table";
import { StatusIndicator } from "@/components/indicator";
import { EmptyState } from "@/components/empty-state";
import { NoticeLine } from "@/components/notice-line";
import { OsMark } from "@/components/os-mark";
import { SectionHead } from "@/components/section-head";
import { Spinner } from "@/components/spinner";
import { api, roleRank, type ImageStatus } from "@/lib/api";
import { useStore } from "@/lib/store";
import { usePending } from "@/lib/use-pending";

/** How far along a download is, as a percentage, or null when unknown. */
function percent(done?: number, total?: number): number | null {
	if (!done || !total) return null;
	return Math.min(100, Math.round((done / total) * 100));
}

function StatusCell({ row }: { row: ImageStatus }) {
	if (row.state === "pulling") {
		const pct = percent(row.done, row.total);
		return (
			<span className="flex items-center gap-2 text-muted-foreground text-sm">
				<Spinner />
				Downloading{pct === null ? "…" : ` ${pct}%`}
			</span>
		);
	}
	if (row.state === "failed") {
		return (
			<span className="flex flex-col gap-0.5">
				<span className="flex items-center gap-2 text-rose-500 text-sm">
					<StatusIndicator color="rose" pulse={false} />
					Download failed
				</span>
				<span className="max-w-md text-muted-foreground text-xs">
					{row.detail}
				</span>
			</span>
		);
	}
	if (row.installed) {
		return (
			<span className="flex items-center gap-2 text-muted-foreground text-sm">
				<StatusIndicator color="emerald" pulse={false} />
				Installed
			</span>
		);
	}
	return (
		<span className="flex items-center gap-2 text-muted-foreground text-sm">
			<StatusIndicator color="slate" pulse={false} />
			Not installed
		</span>
	);
}

function ActionCell({
	row,
	isAdmin,
	busy,
	onPull,
}: {
	row: ImageStatus;
	isAdmin: boolean;
	busy: boolean;
	onPull: (name: string) => void;
}) {
	if (row.installed) return null;
	if (!row.pullable) {
		// Defined, but nothing to download: it has to be built from a checkout.
		return (
			<span className="text-muted-foreground text-xs">Build it on the host</span>
		);
	}
	if (!isAdmin) {
		// Installing an image changes the daemon for everyone.
		return <span className="text-muted-foreground text-xs">ask an admin</span>;
	}
	return (
		<Button
			disabled={busy || row.state === "pulling"}
			onClick={() => onPull(row.name)}
			size="sm"
			variant={row.state === "failed" ? "outline" : "default"}
		>
			<DownloadIcon />
			{row.state === "failed" ? "Retry" : "Download"}
		</Button>
	);
}

export function ImagesPage() {
	const { images, catalogue, me, status } = useStore();
	const defaultImage = status?.default_image;
	const [error, setError] = useState<string>();
	const { pending: busy, run } = usePending();
	const isAdmin = roleRank(me?.role) >= roleRank("admin");

	// Fall back to the plain name list if this daemon predates the catalogue
	// (a browser holding a cached bundle is the only way to see that).
	const rows: ImageStatus[] = catalogue.length
		? catalogue
		: images.map((name) => ({ name, state: "installed", installed: true }));

	async function pull(name: string) {
		setError(undefined);
		await run(async () => {
			try {
				await api.pullImage(name);
			} catch (e) {
				setError(
					e instanceof Error ? `Could not start the download: ${e.message}` : "Could not start the download",
				);
			}
		});
	}

	return (
		<div className="space-y-6">
			<SectionHead
				badge={<Badge variant="secondary">{rows.length}</Badge>}
				desc="Guest images this daemon can boot, and the ones this build knows about but has not downloaded yet. The default is used when a request doesn't name one."
				title="Images"
			/>
			<NoticeLine message={error} />
			<Card className="shadow-none dark:ring-0">
				<CardContent className="p-0">
					{rows.length === 0 ? (
						<EmptyState
							hint="Run `warmbox image build <name>` on the host to see what is installed."
							title="No images registered"
						/>
					) : (
						<Table>
							<TableHeader>
								<TableRow className="hover:bg-transparent">
									<TableHead className="pl-6">Image</TableHead>
									<TableHead>Status</TableHead>
									<TableHead className="pr-6 text-right">Actions</TableHead>
								</TableRow>
							</TableHeader>
							<TableBody>
								{rows.map((row) => {
									const isDefault = row.name === defaultImage;
									return (
										<TableRow className="h-14 hover:bg-transparent" key={row.name}>
											<TableCell className="pl-6">
												<span className="flex items-center gap-3 font-medium">
													<span
														aria-hidden="true"
														className="flex size-8 shrink-0 items-center justify-center rounded-md border bg-muted text-muted-foreground [&_svg]:size-4"
													>
														<OsMark className="size-5" name={row.name} />
													</span>
													<span className="truncate">{row.name}</span>
													{isDefault ? <Badge>Default</Badge> : null}
													{row.headless ? (
														<Badge variant="secondary">Headless</Badge>
													) : null}
												</span>
											</TableCell>
											<TableCell>
												<StatusCell row={row} />
											</TableCell>
											<TableCell className="pr-6 text-right">
												<ActionCell
													busy={busy}
													isAdmin={isAdmin}
													onPull={(name) => void pull(name)}
													row={row}
												/>
											</TableCell>
										</TableRow>
									);
								})}
							</TableBody>
						</Table>
					)}
				</CardContent>
			</Card>
		</div>
	);
}
