import { useEffect, useState, type FormEvent } from "react";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { NoticeLine } from "@/components/notice-line";
import { SectionHead } from "@/components/section-head";
import { Spinner } from "@/components/spinner";
import { api, ApiError } from "@/lib/api";
import { usePending } from "@/lib/use-pending";

/** The daemon-wide desktop ceiling: how much memory one workspace may hold. */
export function DesktopLimitSection() {
	const [limit, setLimit] = useState("");
	const [usage, setUsage] = useState<number>();
	const [error, setError] = useState<string>();
	const [done, setDone] = useState<string>();
	const { pending, run } = usePending();

	useEffect(() => {
		void (async () => {
			try {
				const res = await api.settings.get();
				setLimit(String(res.max_desktops_per_workspace));
				setUsage(res.desktops_in_workspace);
			} catch (err) {
				setError(
					err instanceof ApiError ? err.message : "could not load the limit",
				);
			}
		})();
	}, []);

	async function save(e: FormEvent) {
		e.preventDefault();
		setError(undefined);
		setDone(undefined);
		const n = Number(limit);
		if (!Number.isInteger(n) || n < 0) {
			setError("Enter 0 (unlimited) or a positive whole number.");
			return;
		}
		await run(async () => {
			try {
				const res = await api.settings.update({
					max_desktops_per_workspace: n,
				});
				setLimit(String(res.max_desktops_per_workspace));
				const after = await api.settings.get();
				setUsage(after.desktops_in_workspace);
				setDone("Saved.");
			} catch (err) {
				setError(
					err instanceof ApiError ? err.message : "could not save the limit",
				);
			}
		});
	}

	return (
		<section className="max-w-2xl space-y-4">
			<SectionHead
				as="h2"
				desc="Every running desktop holds its memory until it is destroyed. The cap keeps one workspace from eating the host."
				title="Desktop limit"
			/>
			<Card className="shadow-none dark:ring-0">
				<CardContent className="px-6 py-2">
					<form className="space-y-4" onSubmit={(e) => void save(e)}>
						<div className="space-y-3">
							<label className="text-sm" htmlFor="max-desktops">
								Desktops per workspace
							</label>
							<Input
								disabled={pending}
								id="max-desktops"
								inputMode="numeric"
								min={0}
								onChange={(e) => setLimit(e.target.value)}
								type="number"
								value={limit}
							/>
							<p className="text-muted-foreground text-xs">
								0 means unlimited. This workspace is running {usage ?? 0}.
							</p>
						</div>
						<NoticeLine className="border-b-0 px-0 py-0" message={error} />
						<NoticeLine
							className="border-b-0 px-0 py-0"
							message={done}
							tone="success"
						/>
						<Button aria-busy={pending} disabled={pending} size="sm" type="submit">
							{pending ? (
								<>
									<Spinner />
									Saving…
								</>
							) : (
								"Save limit"
							)}
						</Button>
					</form>
				</CardContent>
			</Card>
		</section>
	);
}
