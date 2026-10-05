import { Cube, ShieldCheck, UserCircle } from "@phosphor-icons/react";

export const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

/**
 * The wizard's three stops. `name`/`blurb` tell the story on the left panel,
 * `heading`/`desc` the form on the right — one source so the two sides can
 * never drift apart.
 */
export const STEPS = [
	{
		icon: Cube,
		name: "Your workspace",
		blurb: "Desktops, volumes and snapshots live here, visible only to its members.",
		heading: "Name your workspace",
		desc: "Pick a name for the first workspace — you become its owner.",
		hint: "You can add more workspaces and members later from Settings.",
	},
	{
		icon: UserCircle,
		name: "Your account",
		blurb: "Name and email — the credentials you sign in with from now on.",
		heading: "Create your login",
		desc: "This account owns the workspace you just named.",
		hint: "Used for the dashboard and `warmbox login` in the terminal.",
	},
	{
		icon: ShieldCheck,
		name: "You own it",
		blurb: "The first account is the owner — invite the team from Settings later.",
		heading: "Set a password",
		desc: "At least 8 characters, then review and create everything at once.",
		hint: "Stored hashed (bcrypt); never logged, never returned.",
	},
];
