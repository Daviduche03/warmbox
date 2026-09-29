import { useState, type FormEvent, type ReactNode } from "react";
import { Button } from "@/components/ui/button";
import {
	Card,
	CardContent,
	CardDescription,
	CardHeader,
	CardTitle,
} from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { LogoIcon } from "@/components/logo";
import { useStore } from "@/lib/store";
import { api, getToken, setToken } from "@/lib/api";

/**
 * Blocks the dashboard until the daemon accepts a token. The daemon issues no
 * sessions — the token is the credential, so this is a connect step, not a login.
 */
export function AuthGate({ children }: { children: ReactNode }) {
	const { unauthorized, refresh } = useStore();
	const [value, setValue] = useState("");
	const [busy, setBusy] = useState(false);
	const [error, setError] = useState<string>();

	if (!unauthorized) return children;

	async function connect(e: FormEvent) {
		e.preventDefault();
		const next = value.trim();
		if (!next || busy) return;
		setBusy(true);
		setError(undefined);
		setToken(next);
		try {
			await api.status();
		} catch {
			setToken("");
			setError("That token was rejected.");
			setBusy(false);
			return;
		}
		setValue("");
		await refresh();
		setBusy(false);
	}

	return (
		<div className="flex min-h-svh items-center justify-center p-6">
			<Card className="w-full max-w-sm shadow-none dark:ring-0">
				<CardHeader className="items-center space-y-3 text-center">
					<span className="flex size-10 items-center justify-center rounded-lg border text-muted-foreground">
						<LogoIcon className="size-5" />
					</span>
					<CardTitle>Connect to warmbox</CardTitle>
					<CardDescription>
						Paste the daemon token to load the dashboard.
					</CardDescription>
				</CardHeader>
				<CardContent>
					<form className="space-y-3" onSubmit={(e) => void connect(e)}>
						<Input
							aria-label="Daemon token"
							autoComplete="off"
							autoFocus
							onChange={(e) => setValue(e.target.value)}
							placeholder="token"
							type="password"
							value={value}
						/>
						{error ? (
							<p className="text-destructive text-sm">{error}</p>
						) : (
							<p className="text-muted-foreground text-xs">
								Found in <code className="font-mono">~/.warmbox/token</code>.
							</p>
						)}
						<Button className="w-full" disabled={!value.trim() || busy} type="submit">
							{busy ? "Connecting…" : "Connect"}
						</Button>
					</form>
					{getToken() ? (
						<p className="mt-3 text-center text-muted-foreground text-xs">
							A stored token was rejected — replace it above.
						</p>
					) : null}
				</CardContent>
			</Card>
		</div>
	);
}
