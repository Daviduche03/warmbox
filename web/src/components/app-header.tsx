"use client";

import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Separator } from "@/components/ui/separator";
import { AppBreadcrumbs } from "@/components/app-breadcrumbs";
import { CustomSidebarTrigger } from "@/components/custom-sidebar-trigger";
import {
	Tooltip,
	TooltipContent,
	TooltipTrigger,
} from "@/components/ui/tooltip";
import { navLinks } from "@/components/app-shared";
import { NavUser } from "@/components/nav-user";
import { useRoute } from "@/lib/router";
import { Plus as PlusIcon, Pulse as PulseIcon } from "@phosphor-icons/react";
import type { ReactNode } from "react";

/** Icon-only header action that names itself on hover. */
function HeaderAction({
	label,
	onClick,
	children,
}: {
	label: string;
	onClick: () => void;
	children: ReactNode;
}) {
	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<Button
					aria-label={label}
					onClick={onClick}
					size="icon-sm"
					variant="outline"
				>
					{children}
				</Button>
			</TooltipTrigger>
			<TooltipContent>{label}</TooltipContent>
		</Tooltip>
	);
}

export function AppHeader() {
	const route = useRoute();
	const current = "#" + route.path;
	const activeItem =
		navLinks.find((item) => item.path === current) ??
		navLinks.find((item) => item.path === "#/");

	return (
		<header
			className={cn(
				"sticky top-0 z-50 flex h-14 shrink-0 items-center justify-between gap-2 border-b px-4 md:px-6"
			)}
		>
			<div className="flex items-center gap-3">
				<CustomSidebarTrigger />
				<Separator
					className="mr-2 h-4 data-[orientation=vertical]:self-center"
					orientation="vertical"
				/>
				<AppBreadcrumbs page={activeItem} />
			</div>
			<div className="flex items-center gap-3">
				<HeaderAction
					label="New desktop"
					onClick={() => (window.location.hash = "/desktops")}
				>
					<PlusIcon />
				</HeaderAction>
				<HeaderAction
					label="Recent activity"
					onClick={() => (window.location.hash = "/activity")}
				>
					<PulseIcon />
				</HeaderAction>
				<Separator
					className="h-4 data-[orientation=vertical]:self-center"
					orientation="vertical"
				/>
				<NavUser />
			</div>
		</header>
	);
}
