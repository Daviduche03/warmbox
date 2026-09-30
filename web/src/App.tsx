import { StoreProvider } from "@/lib/store";
import { TooltipProvider } from "@/components/ui/tooltip";
import { AppShell } from "@/components/app-shell";
import { AuthGate } from "@/components/auth-gate";
import { Dashboard } from "@/components/dashboard";
import { EmptyState } from "@/components/empty-state";
import { useRoute } from "@/lib/router";
import { DesktopsPage } from "@/pages/Desktops";
import { VolumesPage } from "@/pages/Volumes";
import { SnapshotsPage } from "@/pages/Snapshots";
import { ImagesPage } from "@/pages/Images";
import { ActivityPage } from "@/pages/Activity";
import { SettingsPage } from "@/pages/Settings";
import { SetupPage } from "@/pages/Setup";
import { LoginPage } from "@/pages/Login";

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
