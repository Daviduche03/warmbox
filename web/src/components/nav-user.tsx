"use client";

import { useState } from "react";
import { Avatar, AvatarFallback } from "@/components/ui/avatar";
import { Button } from "@/components/ui/button";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuGroup,
	DropdownMenuItem,
	DropdownMenuLabel,
	DropdownMenuSeparator,
	DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { LogoIcon } from "@/components/logo";
import { getToken } from "@/lib/api";
import { useStore } from "@/lib/store";
import { Check as CheckIcon, Copy as CopyIcon, GearSix as SettingsIcon } from "@phosphor-icons/react";

/**
 * Host identity in the header. warmbox has no accounts — this is the daemon
 * behind the UI, so the menu reports what it is rather than who is signed in.
 */
export function NavUser() {
	const { status } = useStore();
	const [copied, setCopied] = useState(false);

	const rows: Array<[string, string]> = [
		["Version", status?.version ?? "—"],
		["Backend", status?.backend ?? "—"],
		["Listen", status?.addr ?? "—"],
		["Uptime", status?.up ?? "—"],
		["Storage", status?.volumes_backed ?? "—"],
	];

	async function copyToken() {
		const token = getToken();
		if (!token) return;
		try {
			await navigator.clipboard.writeText(token);
			setCopied(true);
			window.setTimeout(() => setCopied(false), 1600);
		} catch {
			/* clipboard unavailable */
		}
	}

	return (
		<DropdownMenu>
			<DropdownMenuTrigger asChild>
				<Button aria-label="Daemon details" size="icon" variant="ghost">
					<Avatar className="size-8 border">
						<AvatarFallback className="bg-muted text-muted-foreground">
							<LogoIcon className="size-4" />
						</AvatarFallback>
					</Avatar>
				</Button>
			</DropdownMenuTrigger>
			<DropdownMenuContent align="end" className="w-64">
				<DropdownMenuLabel className="flex items-center gap-3">
					<Avatar className="size-9 border">
						<AvatarFallback className="bg-muted text-muted-foreground">
							<LogoIcon className="size-4" />
						</AvatarFallback>
					</Avatar>
					<div className="min-w-0">
						<span className="font-medium text-foreground">warmbox daemon</span>
						<div className="truncate text-muted-foreground text-xs">
							{status ? `${status.backend} · ${status.addr}` : "connecting…"}
						</div>
					</div>
				</DropdownMenuLabel>
				<DropdownMenuSeparator />
				<DropdownMenuGroup>
					{rows.map(([label, value]) => (
						<DropdownMenuItem disabled key={label}>
							<span className="text-muted-foreground">{label}</span>
							<span className="ml-auto tabular-nums">{value}</span>
						</DropdownMenuItem>
					))}
				</DropdownMenuGroup>
				<DropdownMenuSeparator />
				<DropdownMenuItem onSelect={() => void copyToken()}>
					{copied ? <CheckIcon /> : <CopyIcon />}
					{copied ? "Token copied" : "Copy API token"}
				</DropdownMenuItem>
				<DropdownMenuSeparator />
				<DropdownMenuItem onSelect={() => (window.location.hash = "/settings")}>
					<SettingsIcon />
					Settings
				</DropdownMenuItem>
			</DropdownMenuContent>
		</DropdownMenu>
	);
}
