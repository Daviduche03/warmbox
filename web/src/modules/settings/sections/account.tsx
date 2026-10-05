import { useState, type FormEvent } from "react";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { SectionHead } from "@/components/section-head";
import { Spinner } from "@/components/spinner";
import { useStore } from "@/lib/stores";
import { api, ApiError } from "@/lib/api";
import { usePending } from "@/lib/use-pending";
import { Notice } from "../ui";

export function AccountSection() {
	const { me, refresh } = useStore();
	const [current, setCurrent] = useState("");
	const [next, setNext] = useState("");
	const [confirm, setConfirm] = useState("");
	const [error, setError] = useState<string>();
	const [done, setDone] = useState<string>();
	const { pending, run } = usePending();

	async function signOut() {
		try {
			await api.auth.logout();
		} catch {
			/* session may already be gone */
		}
		await refresh();
	}

	async function changePassword(e: FormEvent) {
		e.preventDefault();
		if (next !== confirm) {
			setError("New passwords do not match.");
			return;
		}
		setError(undefined);
		setDone(undefined);
		await run(async () => {
			try {
				await api.auth.changePassword({ current, next });
				setCurrent("");
				setNext("");
				setConfirm("");
				setDone("Password updated.");
			} catch (err) {
				setError(err instanceof ApiError ? err.message : "update failed");
			}
		});
	}

	return (
		<div className="max-w-2xl space-y-6">
			<SectionHead
				action={
					<Button onClick={() => void signOut()} size="sm" variant="outline">
						Sign out
					</Button>
				}
				desc={
					me ? (
						<>
							Signed in as <span className="text-foreground">{me.user.name}</span>{" "}
							({me.user.email}) · {me.role} of {me.workspace.name}.
						</>
					) : (
						"Your session."
					)
				}
				title="Account"
			/>
			<Card className="shadow-none dark:ring-0">
				<CardContent className="px-6 py-2">
					<form className="space-y-4" onSubmit={(e) => void changePassword(e)}>
						<p className="font-medium text-sm">Change password</p>
						<Input
							aria-label="Current password"
							autoComplete="current-password"
							disabled={pending}
							onChange={(e) => setCurrent(e.target.value)}
							placeholder="Current password"
							type="password"
							value={current}
						/>
						<div className="flex flex-col gap-3 sm:flex-row">
							<Input
								aria-label="New password"
								autoComplete="new-password"
								disabled={pending}
								onChange={(e) => setNext(e.target.value)}
								placeholder="New password (8+ characters)"
								type="password"
								value={next}
							/>
							<Input
								aria-label="Confirm new password"
								autoComplete="new-password"
								disabled={pending}
								onChange={(e) => setConfirm(e.target.value)}
								placeholder="Confirm"
								type="password"
								value={confirm}
							/>
						</div>
						<Notice message={error} />
						<Notice message={done} tone="success" />
						<Button
							aria-busy={pending}
							disabled={!current || next.length < 8 || pending}
							size="sm"
							type="submit"
							variant="outline"
						>
							{pending ? (
								<>
									<Spinner />
									Updating…
								</>
							) : (
								"Update password"
							)}
						</Button>
					</form>
				</CardContent>
			</Card>
		</div>
	);
}
