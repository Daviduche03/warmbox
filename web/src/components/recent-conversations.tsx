import { cn } from "@/lib/utils";
import type { ComponentProps } from "react";
import { Button } from "@/components/ui/button";
import {
	Card,
	CardContent,
	CardDescription,
	CardHeader,
	CardTitle,
} from "@/components/ui/card";
import {
	Table,
	TableBody,
	TableCell,
	TableHead,
	TableHeader,
	TableRow,
} from "@/components/ui/table";
import { StateBadge } from "@/components/state-badge";
import { EmptyState } from "@/components/empty-state";
import { useStore } from "@/lib/store";
import { uptime } from "@/lib/format";
import { ArrowRight as ArrowRightIcon } from "@phosphor-icons/react";

/** Live desktops — the same rows the Desktops page lists, scoped to a preview. */
export function RecentConversations({
	className,
	...props
}: ComponentProps<typeof Card>) {
	const { desktops } = useStore();

	return (
		<Card
			className={cn("gap-0 shadow-none md:col-span-2 dark:ring-0", className)}
			{...props}
		>
			<CardHeader className="border-b">
				<CardTitle>Desktops</CardTitle>
				<CardDescription>
					{desktops.length} running now, sampled live
				</CardDescription>
			</CardHeader>
			<CardContent className="p-0">
				{desktops.length === 0 ? (
					<EmptyState
						hint="Create one and it appears here within a few seconds."
						title="Nothing running"
					/>
				) : (
					<Table>
						<TableHeader>
							<TableRow className="hover:bg-transparent">
								<TableHead className="pl-6">Desktop</TableHead>
								<TableHead className="hidden sm:table-cell">Guest IP</TableHead>
								<TableHead className="hidden md:table-cell">Uptime</TableHead>
								<TableHead className="hidden lg:table-cell">Volume</TableHead>
								<TableHead className="pr-6 text-right">State</TableHead>
							</TableRow>
						</TableHeader>
						<TableBody>
							{desktops.map((d) => (
								<TableRow className="h-14 hover:bg-transparent" key={d.id}>
									<TableCell className="max-w-36 truncate pl-6 font-mono font-medium text-sm">
										{d.id}
									</TableCell>
									<TableCell className="hidden text-muted-foreground text-sm sm:table-cell">
										<span className="tabular-nums">{d.guest_ip ?? "—"}</span>
									</TableCell>
									<TableCell className="hidden text-muted-foreground text-sm md:table-cell">
										<span className="tabular-nums">{uptime(d.started)}</span>
									</TableCell>
									<TableCell className="hidden max-w-32 text-muted-foreground text-sm lg:table-cell">
										<span className="line-clamp-1">{d.volume ?? "—"}</span>
									</TableCell>
									<TableCell className="pr-6 text-right">
										<StateBadge state={d.state} />
									</TableCell>
								</TableRow>
							))}
						</TableBody>
					</Table>
				)}
				<div className="flex justify-center border-t py-3">
					<Button asChild size="sm" variant="ghost">
						<a href="/desktops">
							Manage desktops
							<ArrowRightIcon aria-hidden="true" data-icon="inline-end" />
						</a>
					</Button>
				</div>
			</CardContent>
		</Card>
	);
}
