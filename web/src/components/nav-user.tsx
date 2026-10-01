"use client";

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
import { useStore } from "@/lib/store";
import { api } from "@/lib/api";
import { navigate } from "@/lib/router";
import { GearSix as SettingsIcon, SignOut } from "@phosphor-icons/react";

/**
 * Who is signed in, plus the daemon behind the UI. The menu reports identity
 * first (name, role) and daemon detail second.
 */
export function NavUser() {
	const { me, status, refresh } = useStore();

	const rows: Array<[string, string]> = [
		["Version", status?.version ?? "—"],
		["Backend", status?.backend ?? "—"],
		["Listen", status?.addr ?? "—"],
		["Uptime", status?.up ?? "—"],
		["Storage", status?.volumes_backed ?? "—"],
	];

	async function signOut() {
		try {
			await api.auth.logout();
		} catch {
			/* session may already be gone */
		}
		await refresh();
	}

	const initials = (me?.user.name ?? me?.user.email ?? "?")
		.split(/[\s@]+/)
		.filter(Boolean)
		.slice(0, 2)
		.map((w) => w[0]?.toUpperCase() ?? "")
		.join("");

	return (
		<DropdownMenu>
			<DropdownMenuTrigger asChild>
				<Button aria-label="Account details" size="icon" variant="ghost">
					<Avatar className="size-8 border">
						<AvatarFallback className="bg-muted text-muted-foreground text-xs font-medium">
							{initials || "?"}
						</AvatarFallback>
					</Avatar>
				</Button>
			</DropdownMenuTrigger>
			<DropdownMenuContent align="end" className="w-64">
				<DropdownMenuLabel className="flex items-center gap-3">
					<Avatar className="size-9 border">
						<AvatarFallback className="bg-muted text-muted-foreground text-xs font-medium">
							{initials || "?"}
						</AvatarFallback>
					</Avatar>
					<div className="min-w-0">
						<span className="font-medium text-foreground">
							{me?.user.name || "Signed in"}
						</span>
						<div className="truncate text-muted-foreground text-xs">
							{[me?.user.email, me?.role].filter(Boolean).join(" · ") ||
								"connecting…"}
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
				<DropdownMenuItem onSelect={() => navigate("/settings")}>
					<SettingsIcon />
					Settings
				</DropdownMenuItem>
				<DropdownMenuItem onSelect={() => void signOut()}>
					<SignOut />
					Sign out
				</DropdownMenuItem>
			</DropdownMenuContent>
		</DropdownMenu>
	);
}
