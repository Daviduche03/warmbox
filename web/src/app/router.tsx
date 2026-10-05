import { useEffect } from "react";
import { StoreProvider } from "@/lib/stores";
import { TooltipProvider } from "@/components/ui/tooltip";
import { AppShell } from "@/components/layout/app-shell";
import { AuthGate } from "@/modules/auth/auth-gate";
import { Dashboard } from "@/modules/dashboard/dashboard.page";
import { EmptyState } from "@/components/empty-state";
import { installRouter, useRoute } from "@/lib/router";
import { DesktopsPage } from "@/modules/desktops/desktops.page";
import { VolumesPage } from "@/modules/volumes/volumes.page";
import { SnapshotsPage } from "@/modules/snapshots/snapshots.page";
import { ImagesPage } from "@/modules/images/images.page";
import { ActivityPage } from "@/modules/activity/activity.page";
import { SettingsPage } from "@/modules/settings/settings.page";
import { SetupPage } from "@/modules/setup/setup.page";
import { LoginPage } from "@/modules/auth/login.page";

function NotFound({ path }: { path: string }) {
	return (
		<div className="shadow-none dark:ring-0">
			<EmptyState
				hint="Use the sidebar, or go back to the overview."
				title={`No page at ${path}`}
			/>
		</div>
	);
}

function Routes() {
	const route = useRoute();

	switch (route.path) {
		case "/":
			return <Dashboard />;
		case "/desktops":
			return <DesktopsPage />;
		case "/volumes":
			return <VolumesPage />;
		case "/snapshots":
			return <SnapshotsPage />;
		case "/images":
			return <ImagesPage />;
		case "/activity":
			return <ActivityPage />;
		case "/settings":
			return <SettingsPage />;
		case "/setup":
			return <SetupPage />;
		case "/login":
			return <LoginPage />;
		default:
			return <NotFound path={route.path} />;
	}
}

export default function App() {
	useEffect(() => installRouter(), []);

	return (
		<StoreProvider>
			<TooltipProvider>
				<AuthGate>
					<AppShell>
						<Routes />
					</AppShell>
				</AuthGate>
			</TooltipProvider>
		</StoreProvider>
	);
}
