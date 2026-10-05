import { useState, type FormEvent } from "react";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/spinner";
import { LogoIcon } from "@/components/logo";
import { useStore } from "@/lib/stores";
import { api, ApiError } from "@/lib/api";
import { cn } from "@/lib/utils";
import { ArrowLeft, ArrowRight, Check, WarningCircle } from "@phosphor-icons/react";
import { EMAIL_RE, STEPS } from "./setup.data";
import { StepFields } from "./step-fields";

/**
 * First-run setup: a split screen — brand story on the left, the three-stop
 * wizard laid straight on the right panel (no card, no frame marks). Only
 * reachable while the daemon has no accounts; the API 404s afterwards.
 */
export function SetupPage() {
	const { refresh } = useStore();
	const [step, setStep] = useState(0);
	const [dir, setDir] = useState<"next" | "prev">("next");
	const [attempted, setAttempted] = useState(false);
	const [workspace, setWorkspace] = useState("");
	const [name, setName] = useState("");
	const [email, setEmail] = useState("");
	const [password, setPassword] = useState("");
	const [confirm, setConfirm] = useState("");
	const [busy, setBusy] = useState(false);
	const [error, setError] = useState<string>();

	/** Validation for the current stop only — fixed live as you type. */
	function stepErrors(s: number): Record<string, string> {
		const e: Record<string, string> = {};
		if (s === 0 && !workspace.trim()) {
			e.workspace = "Workspace name is required.";
		}
		if (s === 1) {
			if (!name.trim()) e.name = "Your name is required.";
			if (!EMAIL_RE.test(email.trim()))
				e.email = "Enter a valid email address.";
		}
		if (s === 2) {
			if (password.length < 8)
				e.password = "Password must be at least 8 characters.";
			if (password !== confirm) e.confirm = "Passwords do not match.";
		}
		return e;
	}

	const show = attempted ? stepErrors(step) : {};

	function go(next: number) {
		setDir(next > step ? "next" : "prev");
		setAttempted(false);
		setError(undefined);
		setStep(next);
	}

	function next() {
		if (Object.keys(stepErrors(step)).length > 0) {
			setAttempted(true);
			return;
		}
		go(step + 1);
	}

	async function submit(e: FormEvent) {
		e.preventDefault();
		if (step < STEPS.length - 1) {
			next();
			return;
		}
		if (busy) return;
		if (Object.keys(stepErrors(2)).length > 0) {
			setAttempted(true);
			return;
		}
		setBusy(true);
		setError(undefined);
		try {
			await api.auth.setup({
				name: name.trim(),
				email: email.trim(),
				password,
				workspace: workspace.trim(),
			});
			await refresh();
		} catch (err) {
			setError(
				err instanceof ApiError ? err.message : "setup failed, try again"
			);
			setBusy(false);
		}
	}

	const current = STEPS[step];
	const last = step === STEPS.length - 1;

	return (
		<div className="grid min-h-svh lg:grid-cols-2">
			{/* Brand panel: the story, with the three stops tracked live. */}
			<div className="hidden flex-col justify-between border-r bg-muted/20 p-10 lg:flex">
				<div className="flex items-center gap-2.5">
					<span className="flex size-9 items-center justify-center rounded-lg border bg-background text-foreground">
						<LogoIcon className="size-5" />
					</span>
					<span className="font-medium tracking-tight text-lg">warmbox</span>
				</div>
				<div className="max-w-md space-y-8">
					<div className="space-y-3">
						<h1 className="font-medium text-3xl tracking-tight text-balance">
							One account. One workspace. Ready to boot.
						</h1>
						<p className="text-muted-foreground">
							Set up the owner account and the first workspace. It takes
							less than a minute.
						</p>
					</div>
					<ul className="space-y-5">
						{STEPS.map((s, i) => {
							const done = step > i;
							const active = step === i;
							return (
								<li
									className={cn(
										"flex gap-3.5 transition-opacity",
										!active && !done && "opacity-50"
									)}
									key={s.name}
								>
									<span
										className={cn(
											"flex size-9 shrink-0 items-center justify-center rounded-lg border bg-background transition-colors",
											active
												? "border-foreground/40 text-foreground"
												: done
													? "text-foreground"
													: "text-muted-foreground"
										)}
									>
										{done ? (
											<Check className="size-4" />
										) : (
											<s.icon className="size-4.5" weight="duotone" />
										)}
									</span>
									<div className="space-y-0.5">
										<p className="text-sm">
											<span className="mr-2 font-mono text-muted-foreground text-xs tabular-nums">
												0{i + 1}
											</span>
											<span
												className={cn(
													"font-medium",
													active && "text-foreground"
												)}
											>
												{s.name}
											</span>
										</p>
										<p className="text-muted-foreground text-sm">{s.blurb}</p>
									</div>
								</li>
							);
						})}
					</ul>
				</div>
				<p className="text-muted-foreground text-xs">
					Self-hosted GUI desktop microVMs.
				</p>
			</div>

			{/* Form panel: heading → progress hairline → fields → actions.
			    Straight on the panel — no card, no corner marks. */}
			<div className="flex items-center justify-center p-6 lg:p-10">
				<div className="w-full max-w-lg">
					<div className="mb-8 flex items-center gap-2.5 lg:hidden">
						<span className="flex size-9 items-center justify-center rounded-lg border text-foreground">
							<LogoIcon className="size-5" />
						</span>
						<span className="font-medium tracking-tight text-lg">warmbox</span>
					</div>
					<form
						className="space-y-6"
						noValidate
						onSubmit={(e) => void submit(e)}
					>
						<div
							className={cn(
								"space-y-6 motion-reduce:animate-none",
								"animate-in fade-in-0 duration-200",
								dir === "next"
									? "slide-in-from-right-3"
									: "slide-in-from-left-3"
							)}
							key={step}
						>
							<div className="space-y-4">
								<h1 className="font-heading font-medium text-2xl tracking-tight">
									{current.heading}
								</h1>
								<div
									aria-label="Setup progress"
									aria-valuemax={STEPS.length}
									aria-valuemin={1}
									aria-valuenow={step + 1}
									className="flex gap-1.5"
									role="progressbar"
								>
									{STEPS.map((s, i) => (
										<span
											className={cn(
												"h-0.5 flex-1 rounded-full transition-colors duration-300",
												i <= step ? "bg-foreground" : "bg-border"
											)}
											key={s.name}
										/>
									))}
								</div>
								<p className="text-muted-foreground text-sm">{current.desc}</p>
							</div>
							<StepFields
								confirm={confirm}
								email={email}
								name={name}
								password={password}
								setConfirm={setConfirm}
								setEmail={setEmail}
								setName={setName}
								setPassword={setPassword}
								setWorkspace={setWorkspace}
								show={show}
								step={step}
								workspace={workspace}
							/>
						</div>
						{error && (
							<p className="flex items-center gap-2 text-destructive text-sm">
								<WarningCircle className="size-4 shrink-0" />
								{error}
							</p>
						)}
						<div className="flex items-center gap-2.5">
							{step > 0 && (
								<Button
									disabled={busy}
									onClick={() => go(step - 1)}
									type="button"
									variant="outline"
								>
									<ArrowLeft className="size-4" />
									Back
								</Button>
							)}
							<Button aria-busy={busy} type="submit">
								{busy ? (
									<>
										<Spinner />
										Creating…
									</>
								) : last ? (
									<>
										Create account
										<ArrowRight className="size-4" />
									</>
								) : (
									<>
										Continue
										<ArrowRight className="size-4" />
									</>
								)}
							</Button>
						</div>
					</form>
				</div>
			</div>
		</div>
	);
}
