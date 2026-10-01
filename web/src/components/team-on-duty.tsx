"use client";

import { cn } from "@/lib/utils";
import type { ComponentProps } from "react";
import { Button } from "@/components/ui/button";
import {
	Card,
	CardContent,
	CardDescription,
	CardHeader,
	CardTitle,
} from "@/components/ui/card";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuLabel,
	DropdownMenuSeparator,
	DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { StatusIndicator } from "@/components/indicator";
import { EmptyState } from "@/components/empty-state";
import { useStore } from "@/lib/store";
import { navigate } from "@/lib/router";
import { Check as CheckIcon, Copy as CopyIcon, DotsThree as EllipsisIcon, Stack as LayersIcon } from "@phosphor-icons/react";

/** Bootable images registered with the daemon, with the default one called out. */
export function TeamOnDuty({
	className,
	...props
}: ComponentProps<typeof Card>) {
	const { images, status } = useStore();
	const defaultImage = status?.default_image;

	async function copyName(name: string) {
		try {
			await navigator.clipboard.writeText(name);
		} catch {
			/* clipboard unavailable */
		}
	}

	return (
		<Card className={cn("shadow-none dark:ring-0", className)} {...props}>
			<CardHeader className="border-b">
				<CardTitle>Images</CardTitle>
				<CardDescription>Bootable images on this daemon</CardDescription>
			</CardHeader>
			<CardContent className="p-0">
				{images.length === 0 ? (
					<EmptyState
						hint="The daemon reports none — check `warmbox images`."
						title="No images registered"
					/>
				) : (
					<ul className="flex flex-col divide-y divide-border">
						{images.map((name) => {
							const isDefault = name === defaultImage;
							return (
								<li
									className="flex items-center gap-2 p-3 first:pt-0 last:pb-0 sm:gap-3"
									key={name}
								>
									<span
										aria-hidden="true"
										className="flex size-8 shrink-0 items-center justify-center rounded-md border bg-muted text-muted-foreground [&_svg]:size-4"
									>
										<LayersIcon />
									</span>
									<div className="min-w-0 flex-1 pr-1">
										<p className="truncate font-medium text-foreground text-sm leading-snug">
											{name}
										</p>
										<p className="flex items-center gap-2 text-[10px] leading-snug">
											<span className="flex shrink-0 items-center gap-1">
												<StatusIndicator color="emerald" pulse={false} />
												Available
											</span>
											<span className="inline-flex size-1 rounded-full bg-foreground/80" />
											<span className="text-muted-foreground">
												{isDefault ? "Default" : "Opt-in"}
											</span>
										</p>
									</div>
									<DropdownMenu>
										<DropdownMenuTrigger asChild>
											<Button
												aria-label={`Actions for ${name}`}
												size="icon-xs"
												variant="ghost"
											>
												<EllipsisIcon />
											</Button>
										</DropdownMenuTrigger>
										<DropdownMenuContent align="end" className="min-w-52">
											<DropdownMenuLabel className="font-normal text-muted-foreground text-xs">
												{name}
											</DropdownMenuLabel>
											<DropdownMenuSeparator />
											<DropdownMenuItem onSelect={() => void copyName(name)}>
												<CopyIcon className="size-4 opacity-70" />
												Copy image name
											</DropdownMenuItem>
											<DropdownMenuItem
												onSelect={() => navigate("/images")}
											>
												<CheckIcon className="size-4 opacity-70" />
												Manage images
											</DropdownMenuItem>
										</DropdownMenuContent>
									</DropdownMenu>
								</li>
							);
						})}
					</ul>
				)}
			</CardContent>
		</Card>
	);
}
