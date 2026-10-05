import { type ReactNode } from "react";
import { cn } from "@/lib/utils";

/** One labelled field: label above, input, then hint or live error below. */
export function Field({
	id,
	label,
	hint,
	error,
	children,
}: {
	id: string;
	label: string;
	hint?: string;
	error?: string;
	children: ReactNode;
}) {
	return (
		<div className="grid gap-1.5">
			<label className="font-medium text-sm" htmlFor={id}>
				{label}
			</label>
			{children}
			{error || hint ? (
				<p
					className={cn(
						"text-xs",
						error ? "text-destructive" : "text-muted-foreground"
					)}
				>
					{error ?? hint}
				</p>
			) : null}
		</div>
	);
}
