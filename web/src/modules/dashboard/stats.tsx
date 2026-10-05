import {
	Card,
	CardContent,
	CardDescription,
	CardHeader,
	CardTitle,
} from "@/components/ui/card";
import { Delta, DeltaIcon, DeltaValue } from "@/modules/dashboard/delta";
import { useStore } from "@/lib/stores";
import { deltaFootnote, windowDelta, type CountKey } from "@/lib/metrics";
import { bytes } from "@/lib/format";

type Stat = {
	key: CountKey;
	label: string;
	value: number;
	context: string;
};

export function DashboardStats() {
	const { desktops, volumes, snapshots, history, status } = useStore();

	const byState = {
		ready: desktops.filter((d) => d.state === "ready").length,
		busy: desktops.filter((d) => d.state === "busy").length,
		booting: desktops.filter((d) => d.state === "booting").length,
		paused: desktops.filter((d) => d.state === "paused").length,
		dead: desktops.filter((d) => d.state === "dead").length,
	};

	const pool = status?.pool ?? { size: 0, idle: 0, booting: 0 };
	const volumeBytes = volumes.reduce((sum, v) => sum + (v.size || 0), 0);
	const snapshotBytes = snapshots.reduce((sum, s) => sum + (s.size || 0), 0);

	const stats: Stat[] = [
		{
			key: "desktops",
			label: "Desktops",
			value: desktops.length,
			context:
				[
					byState.ready ? `${byState.ready} ready` : undefined,
					byState.busy ? `${byState.busy} busy` : undefined,
					byState.booting ? `${byState.booting} booting` : undefined,
					byState.paused ? `${byState.paused} paused` : undefined,
					byState.dead ? `${byState.dead} dead` : undefined,
				]
					.filter(Boolean)
					.join(" · ") || "none running",
		},
		{
			key: "idle",
			label: "Idle pool",
			value: pool.idle,
			context:
				pool.size > 0
					? `of ${pool.size} warm slot${pool.size === 1 ? "" : "s"}`
					: "pool not sized",
		},
		{
			key: "volumes",
			label: "Volumes",
			value: volumes.length,
			context: `${bytes(volumeBytes)} provisioned`,
		},
		{
			key: "snapshots",
			label: "Snapshots",
			value: snapshots.length,
			context: `${bytes(snapshotBytes)} stored`,
		},
	];

	return (
		<>
			{stats.map((s) => {
				const delta = windowDelta(history, s.key);
				return (
					<Card className="shadow-none dark:ring-0" key={s.label}>
						<CardHeader>
							<CardTitle className="font-normal text-muted-foreground text-xs">
								{s.label}
							</CardTitle>
							<CardDescription className="truncate text-xs">
								{s.context}
							</CardDescription>
						</CardHeader>
						<CardContent className="flex flex-col gap-2">
							<p className="font-semibold text-2xl tabular-nums">{s.value}</p>
							{delta === null ? (
								<p className="text-muted-foreground text-xs">
									{deltaFootnote(history, "collecting samples")}
								</p>
							) : delta === 0 ? (
								<p className="text-muted-foreground text-xs">
									no change · {deltaFootnote(history, "across the window")}
								</p>
							) : (
								<div className="flex items-center gap-1 text-xs">
									<Delta value={delta}>
										<DeltaIcon />
										<DeltaValue precision={0} suffix="" />
									</Delta>
									<span className="text-muted-foreground">
										{deltaFootnote(history, "across the window")}
									</span>
								</div>
							)}
						</CardContent>
					</Card>
				);
			})}
		</>
	);
}
