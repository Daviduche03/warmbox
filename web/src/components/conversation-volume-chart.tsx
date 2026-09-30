"use client";

import { cn } from "@/lib/utils";
import { type ComponentProps, useId, useMemo, useState } from "react";
import { Area, AreaChart, CartesianGrid, XAxis, YAxis } from "recharts";
import {
	Card,
	CardContent,
	CardDescription,
	CardHeader,
	CardTitle,
} from "@/components/ui/card";
import {
	type ChartConfig,
	ChartContainer,
	ChartTooltip,
	ChartTooltipContent,
} from "@/components/ui/chart";
import {
	Select,
	SelectContent,
	SelectItem,
	SelectTrigger,
	SelectValue,
} from "@/components/ui/select";
import { Delta, DeltaIcon, DeltaValue } from "@/components/delta";
import { EmptyState } from "@/components/empty-state";
import { useStore } from "@/lib/store";
import { windowDelta, windowSpanMs, shortDuration, integerAxis } from "@/lib/metrics";

/** 0 keeps the whole session; the other values are lookback windows in seconds. */
type Range = 0 | 300 | 60;

type ActivityRow = {
	t: number;
	ready: number;
	paused: number;
	booting: number;
};

function timeLabel(t: number, withSeconds = false): string {
	const d = new Date(t);
	const hh = String(d.getHours()).padStart(2, "0");
	const mm = String(d.getMinutes()).padStart(2, "0");
	if (!withSeconds) return `${hh}:${mm}`;
	return `${hh}:${mm}:${String(d.getSeconds()).padStart(2, "0")}`;
}

const chartConfig = {
	ready: { label: "Ready", color: "var(--chart-1)" },
	paused: { label: "Paused", color: "var(--chart-2)" },
	booting: { label: "Booting", color: "var(--chart-3)" },
} satisfies ChartConfig;

export function ConversationVolumeChart({
	className,
	...props
}: ComponentProps<typeof Card>) {
	const chartUid = useId().replace(/:/g, "");
	const idAreaGradient = `pool-activity-area-grad-${chartUid}`;

	const [range, setRange] = useState<Range>(0);
	const { history, desktops } = useStore();

	const chartRows = useMemo<ActivityRow[]>(() => {
		if (range === 0) return history;
		const newest = history[history.length - 1]?.t ?? 0;
		const cutoff = newest - range * 1000;
		return history.filter((s) => s.t >= cutoff);
	}, [history, range]);

	const growth = windowDelta(history, "desktops");
	const span = windowSpanMs(chartRows);
	// Minutes repeat while the window is short — show seconds until it doesn't.
	const withSeconds = span !== null && span < 180_000;
	const axis = integerAxis(
		Math.max(0, ...chartRows.map((r) => r.ready + r.paused + r.booting))
	);

	// One sample draws as nothing at all — say so rather than leave a blank frame.
	if (chartRows.length < 2) {
		return (
			<Card
				className={cn(
					"shadow-none md:col-span-2 lg:col-span-3 dark:ring-0",
					className
				)}
				{...props}
			>
				<CardHeader className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
					<div className="min-w-0 space-y-2">
						<CardTitle>Pool activity</CardTitle>
						<CardDescription>Desktops by state, sampled live.</CardDescription>
					</div>
				</CardHeader>
				<CardContent>
					<EmptyState
						hint="The daemon samples every few seconds — the line appears after the second reading."
						title="Collecting samples"
					/>
				</CardContent>
			</Card>
		);
	}

	return (
		<Card
			className={cn(
				"shadow-none md:col-span-2 lg:col-span-3 dark:ring-0",
				className
			)}
			{...props}
		>
			<CardHeader className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
				<div className="min-w-0 space-y-2">
					<div className="flex flex-wrap items-center gap-2">
						<CardTitle>Pool activity</CardTitle>
						<Delta value={growth ?? 0} variant="badge">
							<DeltaIcon variant="trend" />
							<DeltaValue precision={0} suffix="" />
						</Delta>
					</div>
					<CardDescription>
						{desktops.length} desktop
						{desktops.length === 1 ? "" : "s"} by state
						{span !== null ? ` · last ${shortDuration(span)}` : ""}.
					</CardDescription>
				</div>
				<Select
					onValueChange={(v) => setRange(Number(v) as Range)}
					value={String(range)}
				>
					<SelectTrigger
						aria-label="Pool activity time range"
						className="w-full min-w-36 sm:w-fit"
						size="sm"
					>
						<SelectValue placeholder="Range" />
					</SelectTrigger>
					<SelectContent align="end">
						<SelectItem value="0">This session</SelectItem>
						<SelectItem value="300">Last 5 minutes</SelectItem>
						<SelectItem value="60">Last 1 minute</SelectItem>
					</SelectContent>
				</Select>
			</CardHeader>
			<CardContent>
				<ChartContainer className="aspect-22/8 w-full" config={chartConfig}>
					<AreaChart
						accessibilityLayer
						data={chartRows}
						margin={{ left: 4, right: 8, top: 8, bottom: 0 }}
					>
						<defs>
							<linearGradient id={idAreaGradient} x1="0" x2="0" y1="0" y2="1">
								<stop
									offset="0%"
									stopColor="var(--color-ready)"
									stopOpacity={0.45}
								/>
								<stop
									offset="55%"
									stopColor="var(--color-ready)"
									stopOpacity={0.12}
								/>
								<stop
									offset="100%"
									stopColor="var(--color-ready)"
									stopOpacity={0}
								/>
							</linearGradient>
						</defs>
						<CartesianGrid className="stroke-border" vertical={false} />
						<XAxis
							axisLine={false}
							dataKey="t"
							interval="preserveStartEnd"
							minTickGap={80}
							tickFormatter={(value) => timeLabel(Number(value), withSeconds)}
							tickLine={false}
							tickMargin={8}
						/>
						<YAxis
							allowDecimals={false}
							axisLine={false}
							domain={[0, axis.domainMax]}
							tick={{ className: "tabular-nums" }}
							tickCount={axis.tickCount}
							tickLine={false}
							tickMargin={8}
							width={36}
						/>
						<ChartTooltip
							content={
								<ChartTooltipContent
									className="min-w-34"
									indicator="line"
									labelFormatter={(_, payload) => {
										const row = payload?.[0]?.payload as ActivityRow | undefined;
										return row ? timeLabel(row.t, true) : "";
									}}
								/>
							}
							cursor={false}
						/>
						<Area
							dataKey="ready"
							dot={false}
							fill={`url(#${idAreaGradient})`}
							isAnimationActive={false}
							stackId="pool"
							stroke="var(--color-ready)"
							strokeWidth={2}
							type="monotone"
						/>
						<Area
							dataKey="paused"
							dot={false}
							fill="var(--color-paused)"
							fillOpacity={0.35}
							isAnimationActive={false}
							stackId="pool"
							stroke="var(--color-paused)"
							strokeWidth={2}
							type="monotone"
						/>
						<Area
							dataKey="booting"
							dot={false}
							fill="var(--color-booting)"
							fillOpacity={0.45}
							isAnimationActive={false}
							stackId="pool"
							stroke="var(--color-booting)"
							strokeWidth={2}
							type="monotone"
						/>
					</AreaChart>
				</ChartContainer>
			</CardContent>
		</Card>
	);
}
