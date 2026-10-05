import { ChannelBreakdownChart } from "@/modules/dashboard/channel-breakdown-chart";
import { ConversationVolumeChart } from "@/modules/dashboard/conversation-volume-chart";
import { DashboardStats } from "@/modules/dashboard/stats";

export function Dashboard() {
	return (
		<div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
			<DashboardStats />
			<ConversationVolumeChart />
			<ChannelBreakdownChart />
		</div>
	);
}
