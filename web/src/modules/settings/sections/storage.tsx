import { useEffect, useState, type FormEvent } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { SectionHead } from "@/components/section-head";
import { Spinner } from "@/components/spinner";
import { StatusIndicator } from "@/components/indicator";
import { api, ApiError, type CloudStorage } from "@/lib/api";
import { usePending } from "@/lib/use-pending";
import { Trash as TrashIcon } from "@phosphor-icons/react";
import { Notice } from "../ui";

/**
 * Where volume bytes live. This is a host-level setting (it decides the storage
 * for every workspace on this daemon), so it is owners-only. Changing it can't
 * take effect until the daemon rebuilds its volume store, so we say that
 * outright rather than pretending it's live.
 */
export function StorageSection() {
	const [cloud, setCloud] = useState<CloudStorage>();
	const [provider, setProvider] = useState("Cloudflare");
	const [bucket, setBucket] = useState("");
	const [endpoint, setEndpoint] = useState("");
	const [region, setRegion] = useState("");
	const [accessKey, setAccessKey] = useState("");
	const [secretKey, setSecretKey] = useState("");
	const [error, setError] = useState<string>();
	const [notice, setNotice] = useState<string>();
	const [confirmClear, setConfirmClear] = useState(false);
	const { pending, run } = usePending();

	async function load() {
		try {
			const res = await api.cloud.get();
			setCloud(res);
			setProvider(res.provider || "Cloudflare");
			setBucket(res.bucket);
			setEndpoint(res.endpoint);
			setRegion(res.region);
		} catch (err) {
			setError(
				err instanceof ApiError ? err.message : "could not load storage settings",
			);
		}
	}

	useEffect(() => {
		void load();
	}, []);

	async function save(e: FormEvent) {
		e.preventDefault();
		if (!bucket.trim() || pending) return;
		setError(undefined);
		setNotice(undefined);
		await run(async () => {
			try {
				await api.cloud.set({
					provider: provider.trim(),
					bucket: bucket.trim(),
					endpoint: endpoint.trim(),
					region: region.trim(),
					access_key: accessKey.trim(),
					secret_key: secretKey.trim(),
				});
				// Keys are write-only, so never echo them back into the form.
				setAccessKey("");
				setSecretKey("");
				setNotice("Saved. Restart the daemon to apply: warmbox service restart");
				await load();
			} catch (err) {
				setError(err instanceof ApiError ? err.message : "save failed");
			}
		});
	}

	async function clear() {
		setError(undefined);
		setNotice(undefined);
		await run(async () => {
			try {
				await api.cloud.clear();
				setBucket("");
				setEndpoint("");
				setRegion("");
				setAccessKey("");
				setSecretKey("");
				setNotice("Cleared. Volumes fall back to local storage after a restart.");
				await load();
			} catch (err) {
				setError(err instanceof ApiError ? err.message : "clear failed");
			}
		});
	}

	return (
		<div className="max-w-2xl space-y-6">
			<SectionHead
				action={
					cloud?.configured ? (
						<Button
							disabled={pending}
							onClick={() => setConfirmClear(true)}
							size="sm"
							variant="outline"
						>
							<TrashIcon />
							Use local storage
						</Button>
					) : undefined
				}
				badge={
					cloud?.configured ? (
						<Badge variant="secondary">
							<StatusIndicator color="emerald" pulse={false} />
							{cloud.bucket}
						</Badge>
					) : (
						<Badge variant="outline">local</Badge>
					)
				}
				desc={
					cloud?.configured ? (
						<>
							Volume bytes go to{" "}
							<span className="text-foreground">{cloud.bucket}</span>. Saved in{" "}
							<code className="font-mono text-xs">{cloud.path}</code>.
						</>
					) : (
						"Volumes are stored on this machine. Point them at a bucket to carry the same disk between machines."
					)
				}
				title="Storage"
			/>
			<Card className="shadow-none dark:ring-0">
				<CardContent className="px-6 py-4">
					<form className="space-y-4" onSubmit={(e) => void save(e)}>
						<div className="grid gap-4 sm:grid-cols-2">
							<div className="space-y-3">
								<label className="text-sm" htmlFor="cloud-provider">
									Provider
								</label>
								<Input
									disabled={pending}
									id="cloud-provider"
									onChange={(e) => setProvider(e.target.value)}
									value={provider}
								/>
							</div>
							<div className="space-y-3">
								<label className="text-sm" htmlFor="cloud-bucket">
									Bucket
								</label>
								<Input
									disabled={pending}
									id="cloud-bucket"
									onChange={(e) => setBucket(e.target.value)}
									placeholder="warmbox"
									value={bucket}
								/>
							</div>
							<div className="space-y-2 sm:col-span-2">
								<label className="text-sm" htmlFor="cloud-endpoint">
									Endpoint
								</label>
								<Input
									disabled={pending}
									id="cloud-endpoint"
									onChange={(e) => setEndpoint(e.target.value)}
									placeholder="https://<account>.r2.cloudflarestorage.com"
									value={endpoint}
								/>
							</div>
							<div className="space-y-3">
								<label className="text-sm" htmlFor="cloud-access">
									Access key
								</label>
								<Input
									disabled={pending}
									id="cloud-access"
									onChange={(e) => setAccessKey(e.target.value)}
									placeholder={cloud?.access_key || "access key id"}
									value={accessKey}
								/>
							</div>
							<div className="space-y-3">
								<label className="text-sm" htmlFor="cloud-secret">
									Secret key
								</label>
								<Input
									disabled={pending}
									id="cloud-secret"
									onChange={(e) => setSecretKey(e.target.value)}
									placeholder={cloud?.secret_key || "secret access key"}
									type="password"
									value={secretKey}
								/>
							</div>
							<div className="space-y-3">
								<label className="text-sm" htmlFor="cloud-region">
									Region
								</label>
								<Input
									disabled={pending}
									id="cloud-region"
									onChange={(e) => setRegion(e.target.value)}
									placeholder="auto"
									value={region}
								/>
							</div>
						</div>
						<p className="text-muted-foreground text-xs">
							Keys are stored once and never shown again — leave them blank to
							keep the current ones.
						</p>
						<Notice message={error} />
						<Notice message={notice} tone="success" />
						<Button
							aria-busy={pending}
							disabled={!bucket.trim() || pending}
							size="sm"
							type="submit"
							variant="outline"
						>
							{pending ? (
								<>
									<Spinner />
									Saving…
								</>
							) : (
								"Save storage settings"
							)}
						</Button>
					</form>
				</CardContent>
			</Card>
			<ConfirmDialog
				description="Volume bytes already in the bucket stay there; new writes go to local disk until you configure a bucket again."
				onConfirm={async () => {
					await clear();
				}}
				onOpenChange={setConfirmClear}
				open={confirmClear}
				title="Use local storage instead?"
			/>
		</div>
	);
}
