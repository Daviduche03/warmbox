import type { ReactNode } from "react";
import { Button } from "@/components/ui/button";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { DotsThree } from "@phosphor-icons/react";

/** Kebab menu for a table row. Callers supply the items. */
export function RowActions({
	label,
	children,
}: {
	label: string;
	children: ReactNode;
}) {
	return (
		<DropdownMenu>
			<DropdownMenuTrigger asChild>
				<Button aria-label={label} size="icon-sm" variant="ghost">
					<DotsThree weight="bold" />
				</Button>
			</DropdownMenuTrigger>
			<DropdownMenuContent align="end" className="min-w-44">
				{children}
			</DropdownMenuContent>
		</DropdownMenu>
	);
}
