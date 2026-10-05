import { useEffect, useState, type FormEvent } from "react";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { DropdownMenuItem } from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { Separator } from "@/components/ui/separator";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { NoticeLine } from "@/components/notice-line";
import { RowActions } from "@/components/row-actions";
import { SectionHead } from "@/components/section-head";
import { Spinner } from "@/components/spinner";
import { api, ApiError } from "@/lib/api";
import { usePending } from "@/lib/use-pending";
import { Check as CheckIcon, Copy as CopyIcon, Plus as PlusIcon, Trash as TrashIcon } from "@phosphor-icons/react";
import { useCopy } from "../settings.hooks";

export function TokensSection() {
	const [tokens, setTokens] = useState<Array<{
		id: string;
		name: string;
		prefix: string;
		created_at: string;
		last_used_at: string | null;
	}>>([]);
	const [name, setName] = useState("");
	const [createOpen, setCreateOpen] = useState(false);
	const [createError, setCreateError] = useState<string>();
	const [created, setCreated] = useState<{ token: string; id: string } | null>(null);
	const [error, setError] = useState<string>();
	const [copied, copy] = useCopy();
	const [pendingDelete, setPendingDelete] = useState<string>();
	const { pending, run } = usePending();

	async function load() {
		try {
			const res = await api.tokens.list();
			setTokens(res.tokens);
		} catch {
			/* listed on mount; failures surface on action */
		}
	}

	useEffect(() => {
		void load();
	}, []);

	async function create(e: FormEvent) {
		e.preventDefault();
		if (!name.trim() || pending) return;
		setCreateError(undefined);
		await run(async () => {
			try {
				const res = await api.tokens.create(name.trim());
				setCreated({ token: res.token, id: res.id });
				setName("");
				setCreateOpen(false);
				await load();
			} catch (err) {
				setCreateError(
					err instanceof ApiError ? err.message : "create failed",
				);
			}
		});
	}

	async function revoke(id: string) {
		setError(undefined);
		try {
			await api.tokens.revoke(id);
			await load();
		} catch (err) {
			setError(err instanceof ApiError ? err.message : "revoke failed");
		}
	}

	return (
		<div className="max-w-2xl space-y-8">
			<section className="space-y-4">
				<SectionHead
					action={
						<Button onClick={() => setCreateOpen(true)} size="sm">
							<PlusIcon />
							New token
						</Button>
					}
					desc={
						<>
							Long-lived credentials for CLI and scripts. Send one as{" "}
							<code className="font-mono">Authorization: Bearer &lt;token&gt;</code>.
						</>
					}
					title="API tokens"
				/>
				<Card className="shadow-none dark:ring-0">
					<CardContent className="p-0">
						<NoticeLine message={error} />
						{created ? (
							<div className="space-y-2 border-b px-6 py-4">
								<p className="text-muted-foreground text-sm">
									Copy it now — it is never shown again.
								</p>
								<div className="flex items-center gap-2">
									<code className="min-w-0 flex-1 truncate rounded-md border bg-muted px-3 py-2 font-mono text-sm">
										{created.token}
									</code>
									<Button
										onClick={() => void copy(created.token)}
										size="sm"
										variant="outline"
									>
										{copied ? <CheckIcon /> : <CopyIcon />}
										{copied ? "Copied" : "Copy"}
									</Button>
								</div>
							</div>
						) : null}
						{tokens.length === 0 ? (
							<p className="text-muted-foreground px-6 py-4 text-sm">
								No tokens yet.
							</p>
						) : (
							<ul className="divide-y divide-border">
								{tokens.map((t) => (
									<li
										className="flex items-center gap-3 px-6 py-2.5"
										key={t.id}
									>
										<div className="min-w-0 flex-1">
											<p className="truncate font-medium text-sm">
												{t.name || "Untitled"}
											</p>
											<p className="font-mono text-muted-foreground text-xs">
												{t.prefix}…
												{t.last_used_at
													? ` · used ${t.last_used_at}`
													: " · never used"}
											</p>
										</div>
										<RowActions label={`Actions for token ${t.name || t.id}`}>
											<DropdownMenuItem
												onSelect={() => setPendingDelete(t.id)}
												variant="destructive"
											>
												<TrashIcon />
												Revoke
											</DropdownMenuItem>
										</RowActions>
									</li>
								))}
							</ul>
						)}
					</CardContent>
				</Card>
				<Dialog
					onOpenChange={(next) => {
						setCreateOpen(next);
						setCreateError(undefined);
					}}
					open={createOpen}
				>
					<DialogContent>
						<DialogHeader>
							<DialogTitle>New API token</DialogTitle>
							<DialogDescription>
								Long-lived credential for the CLI and scripts. You'll see it
								once, right after it's created.
							</DialogDescription>
						</DialogHeader>
						<form
							className="space-y-4"
							onSubmit={(e) => void create(e)}
						>
							<div className="space-y-3">
								<label className="text-sm" htmlFor="new-token-name">
									Name
								</label>
								<Input
									autoFocus
									disabled={pending}
									id="new-token-name"
									onChange={(e) => setName(e.target.value)}
									placeholder="e.g. deploy script"
									value={name}
								/>
							</div>
							<NoticeLine
								className="border-b-0 px-0 py-0"
								message={createError}
							/>
							<DialogFooter>
								<Button
									disabled={pending}
									onClick={() => setCreateOpen(false)}
									size="sm"
									type="button"
									variant="outline"
								>
									Cancel
								</Button>
								<Button
									aria-busy={pending}
									disabled={!name.trim() || pending}
									size="sm"
									type="submit"
								>
									{pending ? (
										<>
											<Spinner />
											Creating…
										</>
									) : (
										<>
											<PlusIcon />
											Create token
										</>
									)}
								</Button>
							</DialogFooter>
						</form>
					</DialogContent>
				</Dialog>
				<ConfirmDialog
					description="Scripts using it stop authenticating immediately."
					onConfirm={async () => {
						if (pendingDelete) await revoke(pendingDelete);
					}}
					onOpenChange={(next) => {
						if (!next) setPendingDelete(undefined);
					}}
					open={!!pendingDelete}
					title="Revoke this token?"
				/>
			</section>

			<Separator />

			<section className="space-y-4">
				<SectionHead
					as="h2"
					desc="Every endpoint below needs a session cookie or a personal token from this tab."
					title="API access"
				/>
				<Card className="shadow-none dark:ring-0">
					<CardContent className="px-6 py-2">
						<div className="space-y-4">
							<p className="text-muted-foreground text-sm">
								Send it as{" "}
								<code className="font-mono">
									Authorization: Bearer &lt;token&gt;
								</code>{" "}
								or append <code className="font-mono">?token=</code> to a
								console URL (the noVNC frames do).
							</p>
							<ul className="space-y-1.5 font-mono text-xs">
								<li>
									<span className="text-muted-foreground">GET </span>/api/status
								</li>
								<li>
									<span className="text-muted-foreground">GET </span>/api/desktops
								</li>
								<li>
									<span className="text-muted-foreground">POST </span>/api/desktops
								</li>
								<li>
									<span className="text-muted-foreground">GET </span>/api/volumes
								</li>
								<li>
									<span className="text-muted-foreground">GET </span>/api/snapshots
								</li>
								<li>
									<span className="text-muted-foreground">GET </span>/api/images
								</li>
								<li>
									<span className="text-muted-foreground">POST </span>
									/api/desktops/&#123;id&#125;/exec
								</li>
							</ul>
						</div>
					</CardContent>
				</Card>
			</section>
		</div>
	);
}
