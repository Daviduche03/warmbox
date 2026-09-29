import { StoreProvider } from "@/lib/store";
import { TooltipProvider } from "@/components/ui/tooltip";
import { AppShell } from "@/components/app-shell";
import { AuthGate } from "@/components/auth-gate";
import { Dashboard } from "@/components/dashboard";
import { EmptyState } from "@/components/empty-state";
import { setToken } from "@/lib/api";
import { useRoute } from "@/lib/router";
import { DesktopsPage } from "@/pages/Desktops";
import { VolumesPage } from "@/pages/Volumes";
import { SnapshotsPage } from "@/pages/Snapshots";
import { ImagesPage } from "@/pages/Images";
import { ActivityPage } from "@/pages/Activity";
import { SettingsPage } from "@/pages/Settings";

// The daemon hands out `?token=` so noVNC iframes can authenticate; capture it
// for API calls before the first poll and drop it from the address bar.
(function seedTokenFromUrl() {
	try {
		const url = new URL(window.location.href);
		const token = url.searchParams.get("token");
		if (!token) return;
		setToken(token);
		url.searchParams.delete("token");
		window.history.replaceState({}, "", url.toString());
	} catch {
		/* no URL to read */
	}
})();

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
