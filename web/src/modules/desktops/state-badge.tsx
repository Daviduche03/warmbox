import { Badge } from "@/components/ui/badge";
import {
	StatusIndicator,
	type StatusIndicatorProps,
} from "@/components/indicator";
import type { DesktopState } from "@/lib/api";

const tone: Record<DesktopState, StatusIndicatorProps["color"]> = {
	ready: "emerald",
	busy: "sky",
	booting: "amber",
	paused: "slate",
	hibernated: "violet",
	dead: "rose",
};

export function StateBadge({ state }: { state: DesktopState }) {
	return (
		<Badge className="gap-1.5 font-normal" variant="outline">
			<StatusIndicator color={tone[state]} pulse={state === "booting"} />
			{state}
		</Badge>
	);
}
