"use client";

import { cn } from "@/lib/utils";
import type { ComponentProps } from "react";
import { Bar, BarChart, Rectangle, XAxis, YAxis } from "recharts";import { Badge } from "@/components/ui/badge";
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
import { EmptyState } from "@/components/empty-state";
import { useStore } from "@/lib/store";
import { bytes } from "@/lib/format";

type VolumeRow = {
	name: string;
	gb: number;
	bytes: number;
};

const chartConfig = {
	gb: {
		label: "Gigabytes",
		color: "var(--chart-1)",
	},
} satisfies ChartConfig;

/** Half of bar width so the ends read as fully rounded "caps". */
const BAR_RADIUS = 5;

function ColumnHoverCursor(props: ComponentProps<typeof Rectangle>) {
	return (
		<Rectangle
			fill="var(--muted)"
			fillOpacity={0.5}
			radius={BAR_RADIUS * 2}
			stroke="none"
			{...props}
		/>
	);
}

export function CsatResponsesChart({
	className,
	...props
}: ComponentProps<typeof Card>) {
	const { volumes } = useStore();

	const rows: VolumeRow[] = [...volumes]
		.sort((a, b) => b.size - a.size)
		.map((v) => ({
			name: v.name,
			gb: Math.round((v.size / 1024 ** 3) * 100) / 100,
			bytes: v.size,
		}));

	const total = volumes.reduce((sum, v) => sum + (v.size || 0), 0);
	const fractional = rows.some((r) => r.gb !== Math.trunc(r.gb));

	return (
		<Card
			className={cn("shadow-none md:col-span-2 dark:ring-0", className)}
			{...props}
		>
			<CardHeader>
				<div className="flex flex-wrap items-center gap-2">
					<CardTitle>Storage</CardTitle>
					<Badge variant="secondary">{bytes(total)}</Badge>
				</div>
				<CardDescription>
					Provisioned size per volume, largest first.
				</CardDescription>
			</CardHeader>
			<CardContent>
				{rows.length === 0 ? (
					<EmptyState
						hint="Create one from the Volumes page and it shows up here."
						title="No volumes yet"
					/>
				) : (
					<ChartContainer className="aspect-video w-full" config={chartConfig}>
						<BarChart
							accessibilityLayer
							data={rows}
							margin={{ top: 8, right: 8, left: -8, bottom: 0 }}
						>
							<XAxis
								axisLine={false}
								dataKey="name"
								interval={0}
								minTickGap={8}
								tickFormatter={(value) => String(value)}
								tickLine={false}
								tickMargin={10}
							/>
							<YAxis
								allowDecimals={fractional}
								axisLine={false}
								domain={[0, "dataMax"]}
								tick={{ className: "tabular-nums" }}
								tickLine={false}
								tickMargin={8}
								width={36}
							/>
							<ChartTooltip
								content={
									<ChartTooltipContent
										formatter={(value) => bytes(Number(value) * 1024 ** 3)}
										hideLabel
									/>
								}
								cursor={<ColumnHoverCursor />}
							/>
							<Bar
								background={{
									fill: "var(--muted)",
									radius: BAR_RADIUS,
								}}
								barSize={28}
								dataKey="gb"
								fill="var(--color-gb)"
								isAnimationActive={false}
								overflow="visible"
								radius={[BAR_RADIUS, BAR_RADIUS, 0, 0]}
							/>
						</BarChart>
					</ChartContainer>
				)}
			</CardContent>
		</Card>
	);
}
