import { useState, type FormEvent } from "react";
import { Button } from "@/components/ui/button";
import {
	Card,
	CardContent,
	CardDescription,
	CardHeader,
	CardTitle,
} from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { NoticeLine } from "@/components/notice-line";
import { Spinner } from "@/components/spinner";
import { LogoIcon } from "@/components/logo";
import { useStore } from "@/lib/store";
import { api, ApiError } from "@/lib/api";

export function LoginPage() {
	const { refresh } = useStore();
	const [email, setEmail] = useState("");
	const [password, setPassword] = useState("");
	const [busy, setBusy] = useState(false);
	const [error, setError] = useState<string>();

	const canSubmit = email.trim().length > 0 && password.length > 0 && !busy;

	async function submit(e: FormEvent) {
		e.preventDefault();
		if (!canSubmit) return;
		setBusy(true);
		setError(undefined);
		try {
			await api.auth.login({ email: email.trim(), password });
			await refresh();
		} catch (err) {
			setError(
				err instanceof ApiError ? err.message : "login failed, try again"
			);
			setBusy(false);
		}
	}

	return (
		<div className="flex min-h-svh items-center justify-center p-6">
			<Card className="w-full max-w-sm shadow-none dark:ring-0">
				<CardHeader className="items-center space-y-3 text-center">
					<span className="flex size-10 items-center justify-center rounded-lg border text-muted-foreground">
						<LogoIcon className="size-5" />
					</span>
					<CardTitle>Log in to warmbox</CardTitle>
					<CardDescription>
						Use the account you created during setup.
					</CardDescription>
				</CardHeader>
				<CardContent>
					<form className="space-y-3" onSubmit={(e) => void submit(e)}>
						<Input
							aria-label="Email"
							autoComplete="email"
							autoFocus
							disabled={busy}
							onChange={(e) => setEmail(e.target.value)}
							placeholder="you@example.com"
							type="email"
							value={email}
						/>
						<Input
							aria-label="Password"
							autoComplete="current-password"
							disabled={busy}
							onChange={(e) => setPassword(e.target.value)}
							placeholder="Password"
							type="password"
							value={password}
						/>
						<NoticeLine message={error} />
						<Button
							aria-busy={busy}
							className="w-full"
							disabled={!canSubmit}
							type="submit"
						>
							{busy ? (
								<>
									<Spinner />
									Logging in…
								</>
							) : (
								"Log in"
							)}
						</Button>
					</form>
				</CardContent>
			</Card>
		</div>
	);
}
