import type { ReactNode } from "react";

/**
 * One labelled control: the label, the control, and (sparingly) one muted line
 * of help. Keeping this in one place is what stops forms from drifting into
 * different label/control rhythms — the label sits a comfortable distance above
 * the control, and hints hang below it.
 */
export function Field({
	id,
	label,
	hint,
	children,
}: {
	/** Ties the label to the control; pass the control's id. */
	id: string;
	label: string;
	hint?: ReactNode;
	children: ReactNode;
}) {
	return (
		<div className="space-y-3">
			<label className="block text-sm" htmlFor={id}>
				{label}
			</label>
			{children}
			{hint ? (
				<p className="text-muted-foreground text-xs">{hint}</p>
			) : null}
		</div>
	);
}
