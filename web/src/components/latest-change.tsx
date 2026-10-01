"use client";

import { cn } from "@/lib/utils";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { useStore } from "@/lib/store";
import { X as XIcon } from "@phosphor-icons/react";

export function LatestChange() {
	const { status } = useStore();
	const [isOpen, setIsOpen] = useState(true);

	const latestChange = {
		badge: status?.version ?? "warmbox",
		title: "Control plane",
		description: "Live daemon state.", // TIP: Use a single line of text for the description. (max 5 words)
		readMore: { href: "/settings", label: "Daemon details" },
	} as const;

	if (!isOpen) {
		return null;
	}

	return (
		<div
			className={cn(
				"frame-crosshair group/latest-change size-full min-h-27 justify-center bg-background",
				"relative flex size-full flex-col gap-1 overflow-hidden px-4 pt-3 pb-1 *:text-nowrap",
				"transition-opacity group-data-[collapsible=icon]:pointer-events-none group-data-[collapsible=icon]:opacity-0"
			)}
		>
			<span className="font-light font-mono text-[10px] text-muted-foreground">
				{latestChange.badge}
			</span>
			<p className="font-medium text-xs">{latestChange.title}</p>
			<span className="text-[10px] text-muted-foreground">
				{latestChange.description}
			</span>
			<Button
				asChild
				className="w-max px-0 font-light text-xs"
				size="sm"
				variant="link"
			>
				<a href={latestChange.readMore.href}>{latestChange.readMore.label}</a>
			</Button>
			<Button
				className="absolute top-2 right-2 z-10 size-6 rounded-full opacity-0 transition-opacity group-hover/latest-change:opacity-100"
				onClick={() => setIsOpen(false)}
				size="icon-sm"
				variant="ghost"
			>
				<XIcon className="size-3.5 text-muted-foreground" />{" "}
			</Button>
		</div>
	);
}
