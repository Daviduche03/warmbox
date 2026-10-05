"use client";

import { cn } from "@/lib/utils";
import type { ComponentProps } from "react";
import { Cell, LabelList, Pie, PieChart } from "recharts";
import { Badge } from "@/components/ui/badge";
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
	ChartLegend,
	ChartLegendContent,
} from "@/components/ui/chart";
import { EmptyState } from "@/components/empty-state";
import { useStore } from "@/lib/stores";
import { pct } from "@/lib/metrics";

type StateKey = "ready" | "paused" | "booting" | "dead";

type StateDatum = {
	state: StateKey;
	count: number;
	fill: string;
};

const chartConfig = {
	ready: { label: "Ready", color: "var(--chart-1)" },
	paused: { label: "Paused", color: "var(--chart-2)" },
	booting: { label: "Booting", color: "var(--chart-3)" },
	dead: { label: "Dead", color: "var(--chart-4)" },
} satisfies ChartConfig;

export function ChannelBreakdownChart({
	className,
	...props
}: ComponentProps<typeof Card>) {
	const { desktops, status } = useStore();

	const count = (state: StateKey) =>
		desktops.filter((d) => d.state === state).length;

	const total = desktops.length;
	const readyCount = count("ready");

	const chartData: StateDatum[] = (
		["ready", "paused", "booting", "dead"] as const
	)
		.map((state) => ({
			state,
			count: count(state),
			fill: `var(--color-${state})`,
		}))
		.filter((d) => d.count > 0);

	const idle = status?.pool.idle ?? 0;

	return (
		<Card
			className={cn("flex flex-col shadow-none dark:ring-0", className)}
			{...props}
		>
			<CardHeader className="items-center space-y-1 pb-0 sm:items-start">
				<div className="flex flex-wrap items-center justify-center gap-2 sm:justify-start">
					<CardTitle>Fleet composition</CardTitle>
					<Badge variant="secondary">
						{total > 0 ? `${pct(readyCount, total).toFixed(0)}% ready` : "empty"}
					</Badge>
				</div>
				<CardDescription>Desktops by state, right now.</CardDescription>
			</CardHeader>
			<CardContent className="my-auto">
				{total === 0 ? (
					<EmptyState
						hint="Start one and the split shows up here."
						title="No desktops running"
					/>
				) : (
					<ChartContainer
						className="mx-auto aspect-square max-h-72 w-full"
						config={chartConfig}
					>
						<PieChart accessibilityLayer>
							<Pie
								cornerRadius={8}
								data={chartData}
								dataKey="count"
								innerRadius={36}
								isAnimationActive={false}
								nameKey="state"
								outerRadius="88%"
								/* A single slice is a full ring — stroking it draws a seam. */
								stroke={chartData.length > 1 ? "var(--card)" : "none"}
								strokeWidth={4}
							>
								{chartData.map((d) => (
									<Cell fill={d.fill} key={d.state} />
								))}
								<LabelList
									className="fill-background font-medium"
									dataKey="count"
									fill="currentColor"
									fontWeight={500}
									formatter={(label) => {
										const n = Number(label);
										const share = pct(n, total);
										// Hide labels on slivers — they collide with the neighbours.
										return Number.isFinite(n) && share >= 12
											? `${share.toFixed(0)}%`
											: "";
									}}
									position="inside"
									stroke="none"
								/>
							</Pie>
							<ChartLegend content={<ChartLegendContent nameKey="state" />} />
						</PieChart>
					</ChartContainer>
				)}
				{total > 0 ? (
					<p className="mt-3 text-center text-muted-foreground text-xs tabular-nums">
						{idle} of {status?.pool.size ?? 0} pool slot
						{(status?.pool.size ?? 0) === 1 ? "" : "s"} idle
					</p>
				) : null}
			</CardContent>
		</Card>
	);
}
