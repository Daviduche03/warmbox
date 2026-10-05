import { useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { SectionHead } from "@/components/section-head";
import { Spinner } from "@/components/spinner";
import { StatusIndicator } from "@/components/indicator";
import { useStore } from "@/lib/stores";
import { ArrowsClockwise } from "@phosphor-icons/react";
import { checkLabel, imageLabel } from "../settings.data";
import { Row, UpdateFlag } from "../ui";

/** Read-only health of the daemon this dashboard is talking to. */
export function DaemonSection() {
	const { me, status, loading, error, refresh } = useStore();
	const [refreshing, setRefreshing] = useState(false);
	// The daemon works this out in the background, so the flag may be missing,
	// still checking, or carrying a failure. All three read better than silence.
	const upd = status?.updates;
	const binUpdate = upd?.binary?.available ? upd.binary : null;
	const imgUpdate = upd?.image?.available ? upd.image : null;
	const check = checkLabel(upd);

	async function runRefresh() {
		setRefreshing(true);
		try {
			await refresh();
		} finally {
			setRefreshing(false);
		}
	}

	return (
		<div className="max-w-2xl space-y-6">
			<SectionHead
				action={
					<Button
						aria-busy={refreshing}
						disabled={refreshing}
						onClick={() => void runRefresh()}
						size="sm"
						variant="outline"
					>
						{refreshing ? <Spinner /> : <ArrowsClockwise />}
						Refresh
					</Button>
				}
				badge={
					error ? (
						<Badge variant="destructive">unreachable</Badge>
					) : status ? (
						<Badge variant="secondary">
							<StatusIndicator color="emerald" pulse={false} />
							connected
						</Badge>
					) : (
						<Badge variant="outline">{loading ? "connecting…" : "idle"}</Badge>
					)
				}
				desc="What this dashboard is talking to."
				title="Daemon"
			/>
			<Card className="shadow-none dark:ring-0">
				<CardContent className="p-0">
					<Row
						label="Version"
						value={status?.version ?? "—"}
						extra={
							binUpdate ? (
								<UpdateFlag href={binUpdate.url}>{binUpdate.latest} available</UpdateFlag>
							) : null
						}
					/>
					<Row label="Backend" value={status?.backend ?? "—"} />
					<Row label="Listen address" value={status?.addr ?? "—"} />
					<Row label="Uptime" value={status?.up ?? "—"} />
					<Row
						label="Storage"
						value={
							status
								? `${status.volumes_backed} · ${status.volumes} volume${status.volumes === 1 ? "" : "s"}`
								: "—"
						}
					/>
					<Row
						label="Warm pool"
						value={
							status
								? `${status.pool.idle} idle · ${status.pool.booting} booting · ${status.pool.size} slot${status.pool.size === 1 ? "" : "s"}`
								: "—"
						}
					/>
					<Row label="Default image" value={status?.default_image ?? "—"} />
					<Row
						label="Guest image"
						value={imageLabel(upd?.image)}
						extra={
							imgUpdate ? (
								<UpdateFlag href={imgUpdate.url}>new build available</UpdateFlag>
							) : null
						}
					/>
					<Row label="Update check" title={check.title} value={check.value} />
					<Row
						label="Signed in"
						value={me ? `${me.user.email} · ${me.role}` : "—"}
					/>
				</CardContent>
			</Card>
		</div>
	);
}
