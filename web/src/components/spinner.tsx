import { CircleNotch } from "@phosphor-icons/react";
import { cn } from "@/lib/utils";

/** Inline activity indicator for a button that is mid-request. */
export function Spinner({ className }: { className?: string }) {
	return (
		<CircleNotch
			aria-hidden="true"
			className={cn("size-3.5 shrink-0 animate-spin", className)}
		/>
	);
}
