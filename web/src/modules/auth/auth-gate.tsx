import type { ReactNode } from "react";
import { Spinner } from "@/components/spinner";
import { useStore } from "@/lib/stores";
import { SetupPage } from "@/modules/setup/setup.page";
import { LoginPage } from "@/modules/auth/login.page";

/**
 * Blocks the dashboard until there is a session. Fresh installs (no accounts
 * yet) get the setup wizard; everyone else gets the login form.
 */
export function AuthGate({ children }: { children: ReactNode }) {
	const { me, needsSetup, loading } = useStore();

	if (needsSetup) return <SetupPage />;
	if (me) return <>{children}</>;
	if (loading) {
		return (
			<div className="flex min-h-svh items-center justify-center">
				<Spinner className="size-5 text-muted-foreground" />
			</div>
		);
	}
	return <LoginPage />;
}
