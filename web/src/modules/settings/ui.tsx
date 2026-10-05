import { type ReactNode } from "react";
import { Badge } from "@/components/ui/badge";
import { NoticeLine } from "@/components/notice-line";

/** Flush notice for bare content — no card bleed, no trailing rule. */
export function Notice({ message, tone }: { message?: string; tone?: "error" | "success" }) {
	return (
		<NoticeLine
			className="border-b-0 px-0 py-0"
			message={message}
			tone={tone}
		/>
	);
}

export function Row({
	label,
	value,
	extra,
	title,
}: {
	label: string;
	value: string;
	/** Rendered after the value — an "update available" badge, say. */
	extra?: ReactNode;
	/** Hover text for the value, used when it is a shortened status. */
	title?: string;
}) {
	return (
		<div className="flex items-center justify-between gap-4 border-b px-6 py-3 last:border-b-0">
			<span className="text-muted-foreground text-sm">{label}</span>
			<span
				className="flex min-w-0 items-center gap-2 truncate font-medium text-sm tabular-nums"
				title={title}
			>
				<span className="truncate">{value}</span>
				{extra}
			</span>
		</div>
	);
}

/** An amber flag linking at the thing that is newer than what we have. */
export function UpdateFlag({ href, children }: { href: string; children: ReactNode }) {
	return (
		<a
			className="shrink-0"
			href={href}
			rel="noreferrer"
			target="_blank"
		>
			<Badge
				className="border-amber-500/50 bg-amber-500/10 text-amber-600 hover:bg-amber-500/20 dark:text-amber-400"
				variant="outline"
			>
				{children}
			</Badge>
		</a>
	);
}
