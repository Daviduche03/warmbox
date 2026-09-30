import { cn } from "@/lib/utils";
import type { ComponentProps } from "react";

/**
 * Placeholder shown inside a widget whose data has not arrived yet — a chart
 * with one sample draws nothing, so say so instead of leaving a blank frame.
 */
export function EmptyState({
	title,
	hint,
	className,
	...props
}: ComponentProps<"div"> & { title: string; hint?: string }) {
	return (
		<div
			className={cn(
				"flex h-full min-h-32 flex-col items-center justify-center gap-1 px-6 text-center",
				className
			)}
			{...props}
		>
			<p className="text-foreground text-sm">{title}</p>
			{hint ? (
				<p className="text-muted-foreground text-xs text-pretty">{hint}</p>
			) : null}
		</div>
	);
}
