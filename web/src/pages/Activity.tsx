import { Badge } from "@/components/ui/badge";
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
import { EmptyState } from "@/components/empty-state";
import { useStore, type Event } from "@/lib/store";
import { ago } from "@/lib/format";
import type { ComponentProps } from "react";

const variant: Record<
	Event["kind"],
	ComponentProps<typeof Badge>["variant"]
> = {
	created: "default",
	state: "secondary",
	gone: "outline",
	error: "destructive",
	volume: "outline",
	snapshot: "outline",
};

function clockAt(t: number): string {
	return new Date(t).toLocaleTimeString("en-US", {
		hour: "2-digit",
		minute: "2-digit",
		second: "2-digit",
	});
}

export function ActivityPage() {
	const { events } = useStore();

	return (
		<Card className="shadow-none dark:ring-0">
			<CardHeader className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
				<div className="min-w-0 space-y-2">
					<div className="flex flex-wrap items-center gap-2">
						<CardTitle>Activity</CardTitle>
						<Badge variant="secondary">{events.length}</Badge>
					</div>
					<CardDescription>
						Transitions the daemon reported since this page loaded. Newest first.
					</CardDescription>
				</div>
			</CardHeader>
			<CardContent className="p-0">
				{events.length === 0 ? (
					<EmptyState
						hint="Created, booting, ready and destroyed transitions land here as they happen."
						title="No activity yet"
					/>
				) : (
					<Table>
						<TableHeader>
							<TableRow className="hover:bg-transparent">
								<TableHead className="pl-6">Time</TableHead>
								<TableHead className="hidden sm:table-cell">When</TableHead>
								<TableHead>Kind</TableHead>
								<TableHead>Event</TableHead>
							</TableRow>
						</TableHeader>
						<TableBody>
							{events.map((e) => (
								<TableRow className="h-12 hover:bg-transparent" key={`${e.t}-${e.message}`}>
									<TableCell className="pl-6 text-muted-foreground text-sm tabular-nums">
										{clockAt(e.t)}
									</TableCell>
									<TableCell className="hidden text-muted-foreground text-sm sm:table-cell tabular-nums">
										{ago(Date.now() - e.t)}
									</TableCell>
									<TableCell>
										<Badge variant={variant[e.kind]}>{e.kind}</Badge>
									</TableCell>
									<TableCell className="max-w-96">
										<span className="line-clamp-1 font-mono text-sm">
											{e.message}
										</span>
									</TableCell>
								</TableRow>
							))}
						</TableBody>
					</Table>
				)}
			</CardContent>
		</Card>
	);
}
