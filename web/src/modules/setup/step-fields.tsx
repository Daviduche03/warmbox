import { Input } from "@/components/ui/input";
import { Field } from "./field";
import { STEPS } from "./setup.data";

/** The current stop's fields, plus the review rows on the last one. */
export function StepFields({
	step,
	show,
	workspace,
	setWorkspace,
	name,
	setName,
	email,
	setEmail,
	password,
	setPassword,
	confirm,
	setConfirm,
}: {
	step: number;
	show: Record<string, string>;
	workspace: string;
	setWorkspace: (v: string) => void;
	name: string;
	setName: (v: string) => void;
	email: string;
	setEmail: (v: string) => void;
	password: string;
	setPassword: (v: string) => void;
	confirm: string;
	setConfirm: (v: string) => void;
}) {
	return (
		<div className="space-y-4">
			{step === 0 && (
				<Field
					error={show.workspace}
					hint={STEPS[0].hint}
					id="setup-workspace"
					label="Workspace name"
				>
					<Input
						aria-invalid={!!show.workspace}
						autoComplete="off"
						autoFocus
						className="h-10"
						id="setup-workspace"
						onChange={(e) => setWorkspace(e.target.value)}
						placeholder="acme"
						value={workspace}
					/>
				</Field>
			)}
			{step === 1 && (
				<>
					<Field
						error={show.name}
						hint="How you appear to teammates."
						id="setup-name"
						label="Your name"
					>
						<Input
							aria-invalid={!!show.name}
							autoComplete="name"
							autoFocus
							className="h-10"
							id="setup-name"
							onChange={(e) => setName(e.target.value)}
							placeholder="Ada Lovelace"
							value={name}
						/>
					</Field>
					<Field
						error={show.email}
						hint="You'll sign in with this."
						id="setup-email"
						label="Email"
					>
						<Input
							aria-invalid={!!show.email}
							autoComplete="email"
							className="h-10"
							id="setup-email"
							onChange={(e) => setEmail(e.target.value)}
							placeholder="you@example.com"
							type="email"
							value={email}
						/>
					</Field>
				</>
			)}
			{step === 2 && (
				<>
					<Field
						error={show.password}
						hint="At least 8 characters."
						id="setup-password"
						label="Password"
					>
						<Input
							aria-invalid={!!show.password}
							autoComplete="new-password"
							autoFocus
							className="h-10"
							id="setup-password"
							onChange={(e) => setPassword(e.target.value)}
							type="password"
							value={password}
						/>
					</Field>
					<Field
						error={show.confirm}
						id="setup-confirm"
						label="Confirm password"
					>
						<Input
							aria-invalid={!!show.confirm}
							autoComplete="new-password"
							className="h-10"
							id="setup-confirm"
							onChange={(e) => setConfirm(e.target.value)}
							type="password"
							value={confirm}
						/>
					</Field>
					{/* Review: plain hairline rows, no box. */}
					<dl className="divide-y divide-border text-sm">
						{[
							["Workspace", workspace.trim() || "—"],
							["Owner", `${name.trim()} · ${email.trim()}`],
							[
								"Password",
								password ? "•".repeat(Math.min(password.length, 16)) : "—",
							],
						].map(([k, v]) => (
							<div
								className="flex items-center justify-between gap-3 py-2"
								key={k}
							>
								<dt className="text-muted-foreground">{k}</dt>
								<dd className="truncate font-medium tabular-nums">{v}</dd>
							</div>
						))}
					</dl>
				</>
			)}
		</div>
	);
}
