import { cn } from "@/lib/utils";
import type { ComponentProps, ReactNode } from "react";
import { Button } from "@/components/ui/button";
import {
	Card,
	CardContent,
	CardDescription,
	CardHeader,
	CardTitle,
} from "@/components/ui/card";
import { EmptyState } from "@/components/empty-state";
import { useStore, type Event } from "@/lib/store";
import { ago } from "@/lib/format";
import {
	Warning as AlertTriangleIcon,
	ArrowRight as ArrowRightIcon,
	Camera as CameraIcon,
	HardDrive as HardDriveIcon,
	Plus as PlusIcon,
	ArrowsClockwise as RefreshCwIcon,
	Trash as TrashIcon,
} from "@phosphor-icons/react";

const iconFor: Record<Event["kind"], ReactNode> = {
	created: <PlusIcon />,
	state: <RefreshCwIcon />,
	gone: <TrashIcon />,
	error: <AlertTriangleIcon />,
	volume: <HardDriveIcon />,
	snapshot: <CameraIcon />,
};

/** Most recent daemon-reported changes, newest first. */
export function SupportActivity({
	className,
	...props
}: ComponentProps<typeof Card>) {
	const { events } = useStore();
	const items = events.slice(0, 6);

	return (
		<Card className={cn("gap-0 shadow-none dark:ring-0", className)} {...props}>
			<CardHeader className="border-b">
				<CardTitle>Recent activity</CardTitle>
				<CardDescription>State changes from the daemon.</CardDescription>
			</CardHeader>
			<CardContent className="px-0">
				{items.length === 0 ? (
					<EmptyState
						hint="Created, booting, ready and destroyed transitions land here."
						title="Quiet so far"
					/>
				) : (
					<ul className="flex flex-col divide-y divide-border">
						{items.map((item) => (
							<li
								className="flex min-h-18 items-center gap-3 px-3 py-2"
								key={`${item.t}-${item.message}`}
							>
								<span
									aria-hidden="true"
									className="flex size-10 shrink-0 items-center justify-center text-muted-foreground [&_svg]:size-4"
								>
									{iconFor[item.kind]}
								</span>
								<div className="min-w-0 flex-1 space-y-1">
									<p className="line-clamp-2 text-pretty text-foreground text-xs leading-snug">
										{item.message}
									</p>
									<p className="text-muted-foreground text-xs tabular-nums">
										{ago(Date.now() - item.t)}
									</p>
								</div>
							</li>
						))}
					</ul>
				)}
			</CardContent>
			<div className="flex items-center justify-center">
				<Button asChild size="sm" variant="ghost">
					<a href="/activity">
						View all
						<ArrowRightIcon aria-hidden="true" data-icon="inline-end" />
					</a>
				</Button>
			</div>
		</Card>
	);
}
