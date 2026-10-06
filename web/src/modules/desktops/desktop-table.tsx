import { Badge } from "@/components/ui/badge";
import { Card, CardContent } from "@/components/ui/card";
import { DropdownMenuItem } from "@/components/ui/dropdown-menu";
import {
	Table,
	TableBody,
	TableCell,
	TableHead,
	TableHeader,
	TableRow,
} from "@/components/ui/table";
import { RowActions } from "@/components/row-actions";
import { StateBadge } from "@/modules/desktops/state-badge";
import { EmptyState } from "@/components/empty-state";
import { NoticeLine } from "@/components/notice-line";
import { uptime } from "@/lib/format";
import type { Desktop } from "@/lib/types";
import {
	ArrowSquareOut as SquareArrowOutUpRightIcon,
	Camera as CameraIcon,
	ClockCounterClockwise as HistoryIcon,
	Globe as GlobeIcon,
	Moon as MoonIcon,
	Pause as PauseIcon,
	Play as PlayIcon,
	Sun as SunIcon,
	Trash as TrashIcon,
} from "@phosphor-icons/react";

type Props = {
	desktops: Desktop[];
	/** Failure of the last destroy/pause/resume/checkpoint/hibernate, shown above the list. */
	error?: string;
	onOpen: (id: string) => void;
	onPause: (id: string) => void;
	onResume: (id: string) => void;
	onHibernate: (id: string) => void;
	onWake: (id: string) => void;
	onCheckpoint: (id: string) => void;
	onCheckpoints: (id: string) => void;
	onPolicy: (d: Desktop) => void;
	onDestroy: (id: string) => void;
};

/** The fleet as it stands: one card, empty state when nothing is running. */
export function DesktopTable({
	desktops,
	error,
	onOpen,
	onPause,
	onResume,
	onHibernate,
	onWake,
	onCheckpoint,
	onCheckpoints,
	onPolicy,
	onDestroy,
}: Props) {
	return (
		<Card className="shadow-none dark:ring-0">
			<CardContent className="p-0">
				<NoticeLine message={error} />
				{desktops.length === 0 ? (
					<EmptyState
						hint="Pick an image in New desktop and boot the first one."
						title="No desktops running"
					/>
				) : (
					<Table>
						<TableHeader>
							<TableRow className="hover:bg-transparent">
								<TableHead className="pl-6">Desktop</TableHead>
								<TableHead className="hidden sm:table-cell">
									Guest IP
								</TableHead>
								<TableHead className="hidden md:table-cell">
									Uptime
								</TableHead>
								<TableHead className="hidden lg:table-cell">
									Volume
								</TableHead>
								<TableHead>State</TableHead>
								<TableHead className="pr-6 text-right">Actions</TableHead>
							</TableRow>
						</TableHeader>
						<TableBody>
							{desktops.map((d) => (
								<TableRow className="h-14 hover:bg-transparent" key={d.id}>
									<TableCell className="max-w-40 truncate pl-6 font-mono font-medium text-sm">
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
									<TableCell>
										<span className="flex items-center gap-2">
											<StateBadge state={d.state} />
											{d.headless ? (
												<Badge variant="outline">Headless</Badge>
											) : null}
										</span>
									</TableCell>
									<TableCell className="pr-6 text-right">
										<RowActions label={`Actions for ${d.id}`}>
											{d.headless ? (
												<DropdownMenuItem disabled>
													<SquareArrowOutUpRightIcon />
													No screen (headless)
												</DropdownMenuItem>
											) : (
												<DropdownMenuItem
													disabled={
														d.state === "dead" ||
														d.state === "paused" ||
														d.state === "hibernated"
													}
													onSelect={() => onOpen(d.id)}
												>
													<SquareArrowOutUpRightIcon />
													Open console
												</DropdownMenuItem>
											)}
											{d.state === "paused" ? (
												<DropdownMenuItem onSelect={() => onResume(d.id)}>
													<PlayIcon />
													Resume
												</DropdownMenuItem>
											) : d.state === "hibernated" ? (
												<DropdownMenuItem onSelect={() => onWake(d.id)}>
													<SunIcon />
													Wake
												</DropdownMenuItem>
											) : (
												<>
													<DropdownMenuItem
														disabled={
															d.state === "booting" || d.state === "dead"
														}
														onSelect={() => onPause(d.id)}
													>
														<PauseIcon />
														Pause
													</DropdownMenuItem>
													<DropdownMenuItem
														disabled={
															d.state === "booting" || d.state === "dead"
														}
														onSelect={() => onHibernate(d.id)}
													>
														<MoonIcon />
														Hibernate
													</DropdownMenuItem>
													<DropdownMenuItem
														disabled={
															d.state === "booting" || d.state === "dead"
														}
														onSelect={() => onCheckpoint(d.id)}
													>
														<CameraIcon />
														Checkpoint now
													</DropdownMenuItem>
													<DropdownMenuItem
														disabled={
															d.state === "booting" || d.state === "dead"
														}
														onSelect={() => onCheckpoints(d.id)}
													>
														<HistoryIcon />
														Checkpoints…
													</DropdownMenuItem>
												</>
											)}
											<DropdownMenuItem onSelect={() => onPolicy(d)}>
												<GlobeIcon />
												Egress policy…
											</DropdownMenuItem>
											<DropdownMenuItem
												onSelect={() => onDestroy(d.id)}
												variant="destructive"
											>
												<TrashIcon />
												Destroy
											</DropdownMenuItem>
										</RowActions>
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
