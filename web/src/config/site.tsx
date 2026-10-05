import type { ReactNode } from "react";
import {
	SquaresFour as LayoutGridIcon,
	Monitor as MonitorIcon,
	HardDrive as HardDriveIcon,
	Camera as CameraIcon,
	Stack as LayersIcon,
	Pulse as ActivityIcon,
	GearSix as SettingsIcon,
} from "@phosphor-icons/react";

export type SidebarNavItem = {
	title: string;
	path?: string;
	icon?: ReactNode;
	isActive?: boolean;
	subItems?: SidebarNavItem[];
};

export type SidebarNavGroup = {
	label?: string;
	items: SidebarNavItem[];
};

/** App paths; `/` is the overview. Active state is resolved against the live route. */
export const navGroups: SidebarNavGroup[] = [
	{
		items: [
			{
				title: "Overview",
				path: "/",
				icon: <LayoutGridIcon />,
			},
		],
	},
	{
		label: "Fleet",
		items: [
			{
				title: "Desktops",
				path: "/desktops",
				icon: <MonitorIcon />,
			},
			{
				title: "Volumes",
				path: "/volumes",
				icon: <HardDriveIcon />,
			},
			{
				title: "Snapshots",
				path: "/snapshots",
				icon: <CameraIcon />,
			},
			{
				title: "Images",
				path: "/images",
				icon: <LayersIcon />,
			},
		],
	},
	{
		label: "Operate",
		items: [
			{
				title: "Activity",
				path: "/activity",
				icon: <ActivityIcon />,
			},
			{
				title: "Settings",
				path: "/settings",
				icon: <SettingsIcon />,
			},
		],
	},
];

/**
 * Deliberately empty: every destination is already in the nav above, and a
 * second entry for the same route would show as two active items at once.
 */
export const footerNavLinks: SidebarNavItem[] = [];

export const navLinks: SidebarNavItem[] = [
	...navGroups.flatMap((group) =>
		group.items.flatMap((item) =>
			item.subItems?.length ? [item, ...item.subItems] : [item]
		)
	),
	...footerNavLinks,
];

/** Marks the item (or one of its sub-items) whose path matches the current route. */
export function withActiveRoute(groups: SidebarNavGroup[], current: string) {
	return groups.map((group) => ({
		...group,
		items: group.items.map((item) => ({
			...item,
			isActive:
				item.path === current ||
				(item.subItems?.some((sub) => sub.path === current) ?? false),
		})),
	}));
}
