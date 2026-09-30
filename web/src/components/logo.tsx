import type React from "react";
import { cn } from "@/lib/utils";

/**
 * warmbox mark — an isometric crate with a solid lid and a small arc rising off
 * it: the pool of microVMs, kept warm and ready to hand out.
 *
 * Drawn in a single colour so it inherits `currentColor` from wherever it sits.
 * Size it from the parent, as the icon sets no width of its own.
 */
export const LogoIcon = ({
	className,
	...props
}: React.ComponentProps<"svg">) => (
	<svg
		className={cn("shrink-0", className)}
		fill="none"
		viewBox="0 0 24 24"
		{...props}
	>
		<path d="M12 6 4 10l8 4 8-4-8-4Z" fill="currentColor" />
		<path
			d="M4 10v7l8 4 8-4v-7"
			stroke="currentColor"
			strokeLinecap="round"
			strokeLinejoin="round"
			strokeWidth={2}
		/>
		<path d="M12 14v7" stroke="currentColor" strokeLinecap="round" strokeWidth={2} />
		<path
			d="M9.2 3.6q2.8-2.4 5.6 0"
			stroke="currentColor"
			strokeLinecap="round"
			strokeWidth={2}
		/>
	</svg>
);
