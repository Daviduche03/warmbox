import type { ReactNode } from "react";

/**
 * The page/section heading that sits OUTSIDE the card: big title (with an
 * optional count badge), one muted line of description, and an optional
 * action pushed to the right. The card below wraps only the table.
 */
export function SectionHead({
	title,
	badge,
	desc,
	action,
	as = "h1",
}: {
	title: string;
	badge?: ReactNode;
	desc: ReactNode;
	action?: ReactNode;
	as?: "h1" | "h2";
}) {
	const Heading = as;
	return (
		<div className="flex items-start justify-between gap-x-6">
			<div className="min-w-0 flex-1 space-y-1.5">
				<div className="flex items-center gap-2.5">
					<Heading className="font-heading text-2xl font-medium tracking-tight">
						{title}
					</Heading>
					{badge}
				</div>
				<p className="text-muted-foreground text-sm">{desc}</p>
			</div>
			{action ? <div className="shrink-0">{action}</div> : null}
		</div>
	);
}
