"use client";

import { cn } from "@/lib/utils";
import type { ComponentProps } from "react";
import {
	CartesianGrid,
	LabelList,
	Line,
	LineChart,
	XAxis,
	YAxis,
} from "recharts";
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
import { Delta, DeltaIcon, DeltaValue } from "@/components/delta";
import { EmptyState } from "@/components/empty-state";
import { useStore } from "@/lib/store";
import { windowDelta, windowSpanMs, shortDuration } from "@/lib/metrics";

function timeLabel(t: number, withSeconds = false): string {
	const d = new Date(t);
	const hh = String(d.getHours()).padStart(2, "0");
	const mm = String(d.getMinutes()).padStart(2, "0");
	if (!withSeconds) return `${hh}:${mm}`;
	return `${hh}:${mm}:${String(d.getSeconds()).padStart(2, "0")}`;
}

const chartConfig = {
	idle: {
		label: "Idle slots",
		color: "var(--chart-2)",
	},
} satisfies ChartConfig;

/** Pool slots available to hand out, sampled live. */
export function FirstReplyTimeChart({
	className,
	...props
}: ComponentProps<typeof Card>) {
	const { history, status } = useStore();

	const rows = history.map((s) => ({ t: s.t, idle: s.idle }));
	const delta = windowDelta(history, "idle");
	const span = windowSpanMs(history);
	const poolSize = Math.max(1, status?.pool.size ?? 1);
	// Minutes repeat while the window is short — show seconds until it doesn't.
	const withSeconds = span !== null && span < 180_000;

	if (rows.length < 2) {
		return (
			<Card
				className={cn("shadow-none md:col-span-2 dark:ring-0", className)}
				{...props}
			>
				<CardHeader className="space-y-1">
					<CardTitle>Warm capacity</CardTitle>
					<CardDescription>Pool slots available to hand out.</CardDescription>
				</CardHeader>
				<CardContent>
					<EmptyState
						hint="A second sample is a few seconds away."
						title="Collecting samples"
					/>
				</CardContent>
			</Card>
		);
	}

	return (
		<Card
			className={cn("shadow-none md:col-span-2 dark:ring-0", className)}
			{...props}
		>
			<CardHeader className="space-y-1">
				<div className="flex flex-wrap items-center gap-2">
					<CardTitle>Warm capacity</CardTitle>
					<Delta value={delta ?? 0} variant="badge">
						<DeltaIcon variant="trend" />
						<DeltaValue precision={0} suffix="" />
					</Delta>
				</div>
				<CardDescription>
					Pool slots available to hand out
					{span !== null ? ` · last ${shortDuration(span)}` : ""}.
				</CardDescription>
			</CardHeader>
			<CardContent>
				<ChartContainer className="aspect-video w-full" config={chartConfig}>
					<LineChart
						accessibilityLayer
						data={rows}
						margin={{ top: 24, left: 20, right: 12, bottom: 8 }}
					>
						<CartesianGrid className="stroke-border" vertical={false} />
						{/* Hidden axis pins the scale to the pool so the line means something. */}
						<YAxis domain={[0, poolSize]} hide />
						<XAxis
							axisLine={false}
							dataKey="t"
							interval="preserveStartEnd"
							minTickGap={80}
							tickFormatter={(value) => timeLabel(Number(value), withSeconds)}
							tickLine={false}
							tickMargin={8}
						/>
						<ChartTooltip
							content={
								<ChartTooltipContent
									indicator="line"
									labelFormatter={(_, payload) => {
										const row = payload?.[0]?.payload as
											| { t: number }
											| undefined;
										return row ? timeLabel(row.t) : "";
									}}
								/>
							}
							cursor={false}
						/>
						<Line
							activeDot={{ r: 6 }}
							dataKey="idle"
							dot={{ fill: "var(--color-idle)" }}
							isAnimationActive={false}
							stroke="var(--color-idle)"
							strokeWidth={2}
							type="monotone"
						>
							<LabelList
								content={({ x, y, value, index }) =>
									index === rows.length - 1 ? (
										<text
											className="fill-foreground"
											fontSize={12}
											textAnchor="middle"
											x={Number(x)}
											y={Number(y ?? 0) - 12}
										>
											{String(value ?? "")}
										</text>
									) : null
								}
								dataKey="idle"
							/>
						</Line>
					</LineChart>
				</ChartContainer>
			</CardContent>
		</Card>
	);
}
