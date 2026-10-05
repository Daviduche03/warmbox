import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useStore } from "@/lib/stores";
import { roleRank } from "@/lib/api";
import { AccountSection } from "./sections/account";
import { DaemonSection } from "./sections/daemon";
import { DesktopLimitSection } from "./sections/desktop-limit";
import { StorageSection } from "./sections/storage";
import { TeamSection } from "./sections/team";
import { TokensSection } from "./sections/tokens";

/**
 * Settings as tabs — account, team, machine credentials, storage, daemon
 * health. Each tab lays its heading straight on the page (setup's split-screen
 * language); tables and row lists sit in a card below it, and every create
 * flow (invite, token) opens in a modal. Team is admin-only and Storage is
 * owners-only: everyone else simply never sees them.
 */
export function SettingsPage() {
	const { me } = useStore();
	const canManage = roleRank(me?.role) >= roleRank("admin");
	const isOwner = me?.role === "owner";

	return (
		<Tabs className="gap-6" defaultValue="account">
			<TabsList>
				<TabsTrigger value="account">Account</TabsTrigger>
				{canManage && <TabsTrigger value="team">Team</TabsTrigger>}
				<TabsTrigger value="tokens">API tokens</TabsTrigger>
				{isOwner && <TabsTrigger value="storage">Storage</TabsTrigger>}
				<TabsTrigger value="daemon">Daemon</TabsTrigger>
			</TabsList>

			<TabsContent value="account">
				<AccountSection />
			</TabsContent>

			{canManage && (
				<TabsContent value="team">
					<TeamSection />
				</TabsContent>
			)}

			<TabsContent value="tokens">
				<TokensSection />
			</TabsContent>

			{isOwner && (
				<TabsContent value="storage">
					<div className="space-y-8">
						<StorageSection />
						<DesktopLimitSection />
					</div>
				</TabsContent>
			)}

			<TabsContent value="daemon">
				<DaemonSection />
			</TabsContent>
		</Tabs>
	);
}
