import { cn } from "@/lib/utils";
import { CheckCircle, WarningCircle } from "@phosphor-icons/react";

/**
 * One-line result notice for a card body: a failure, or confirmation of an
 * action that had no dialog of its own. Renders nothing without a message.
 */
export function NoticeLine({
	message,
	tone = "error",
	className,
}: {
	message?: string;
	tone?: "error" | "success";
	/** Extra classes — e.g. `"border-b-0 px-0"` when sitting on bare content. */
	className?: string;
}) {
	if (!message) return null;
	const Icon = tone === "error" ? WarningCircle : CheckCircle;
	return (
		<p
			className={cn(
				"flex items-center gap-2 border-b px-6 py-2 text-sm",
				tone === "error" ? "text-destructive" : "text-muted-foreground",
				className
			)}
		>
			<Icon className="size-4 shrink-0" />
			{message}
		</p>
	);
}
