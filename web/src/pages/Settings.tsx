import { useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { StatusIndicator } from "@/components/indicator";
import { Spinner } from "@/components/spinner";
import { useStore } from "@/lib/store";
import { getToken } from "@/lib/api";
import { usePending } from "@/lib/use-pending";
import {
	ArrowsClockwise,
	Check as CheckIcon,
	Copy as CopyIcon,
} from "@phosphor-icons/react";

function Row({ label, value }: { label: string; value: string }) {
	return (
		<div className="flex items-center justify-between gap-4 border-b px-6 py-3 last:border-b-0">
			<span className="text-muted-foreground text-sm">{label}</span>
			<span className="truncate font-medium text-sm tabular-nums">{value}</span>
		</div>
	);
}

export function SettingsPage() {
	const { status, loading, error, refresh } = useStore();
	const [copied, setCopied] = useState(false);
	const { pending: refreshing, run } = usePending();

	async function copyToken() {
		const token = getToken();
		if (!token) return;
		try {
			await navigator.clipboard.writeText(token);
			setCopied(true);
			window.setTimeout(() => setCopied(false), 1600);
		} catch {
			/* clipboard unavailable */
		}
	}

	const token = getToken();
	const masked = token
		? `${token.slice(0, 6)}${"•".repeat(12)}${token.slice(-4)}`
		: "not set";

	return (
		<div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
			<Card className="shadow-none dark:ring-0">
				<CardHeader className="border-b">
					<div className="flex flex-wrap items-center gap-2">
						<CardTitle>Daemon</CardTitle>
						{error ? (
							<Badge variant="destructive">unreachable</Badge>
						) : status ? (
							<Badge variant="secondary">
								<StatusIndicator color="emerald" pulse={false} />
								connected
							</Badge>
						) : (
							<Badge variant="outline">
								{loading ? "connecting…" : "idle"}
							</Badge>
						)}
					</div>
					<CardDescription>What this dashboard is talking to.</CardDescription>
					<CardAction>
						<Button
							aria-busy={refreshing}
							disabled={refreshing}
							onClick={() => void run(refresh)}
							size="sm"
							variant="outline"
						>
							{refreshing ? <Spinner /> : <ArrowsClockwise />}
							Refresh
						</Button>
					</CardAction>
				</CardHeader>
				<CardContent className="p-0">
					<Row label="Version" value={status?.version ?? "—"} />
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
						label="Token required"
						value={status ? (status.token_required ? "yes" : "no") : "—"}
					/>
				</CardContent>
			</Card>

			<Card className="shadow-none dark:ring-0">
				<CardHeader className="border-b">
					<CardTitle>API access</CardTitle>
				</CardHeader>
				<CardContent className="space-y-4 p-6">
					<div className="space-y-2">
						<p className="text-muted-foreground text-sm">Access token</p>
						<div className="flex items-center gap-2">
							<code className="min-w-0 flex-1 truncate rounded-md border bg-muted px-3 py-2 font-mono text-sm">
								{masked}
							</code>
							<Button
								disabled={!token}
								onClick={() => void copyToken()}
								size="sm"
								variant="outline"
							>
								{copied ? <CheckIcon /> : <CopyIcon />}
								{copied ? "Copied" : "Copy"}
							</Button>
						</div>
						<p className="text-muted-foreground text-xs">
							Stored on this machine at{" "}
							<code className="font-mono">~/.warmbox/token</code>. Send it as{" "}
							<code className="font-mono">Authorization: Bearer &lt;token&gt;</code>{" "}
							or append <code className="font-mono">?token=</code> to a URL when a
							page needs it (the noVNC frames do).
						</p>
					</div>
					<div className="space-y-2">
						<p className="text-muted-foreground text-sm">Endpoints</p>
						<ul className="space-y-1 font-mono text-xs">
							<li>
								<span className="text-muted-foreground">GET </span>/api/status
							</li>
							<li>
								<span className="text-muted-foreground">GET </span>/api/desktops
							</li>
							<li>
								<span className="text-muted-foreground">POST </span>/api/desktops
							</li>
							<li>
								<span className="text-muted-foreground">GET </span>/api/volumes
							</li>
							<li>
								<span className="text-muted-foreground">GET </span>/api/snapshots
							</li>
							<li>
								<span className="text-muted-foreground">GET </span>/api/images
							</li>
							<li>
								<span className="text-muted-foreground">POST </span>
								/api/desktops/&#123;id&#125;/exec
							</li>
						</ul>
					</div>
				</CardContent>
			</Card>
		</div>
	);
}
