import { type UpdateCheck } from "@/lib/api";

/** The guest-image row: what is installed, and whether a newer one is out. */
export function imageLabel(img?: UpdateCheck["image"]): string {
	if (!img) return "—";
	if (img.available) return `${img.current?.slice(0, 7) ?? "?"} installed`;
	if (!img.latest) return "couldn't check";
	if (!img.known) return img.current === "local" ? "built locally" : "not recorded";
	return `${img.current?.slice(0, 7)} · up to date`;
}

/** Freshness of the check itself, so a quiet flag is never mistaken for a stale one. */
export function checkLabel(upd?: UpdateCheck): { value: string; title?: string } {
	if (!upd) return { value: "—" };
	if (upd.error) return { value: "couldn't check", title: upd.error };
	if (!upd.checked_at) return { value: "checking…" };
	return { value: `checked ${ago(upd.checked_at)}` };
}

function ago(iso: string): string {
	const secs = Math.max(0, (Date.now() - Date.parse(iso)) / 1000);
	if (secs < 90) return "just now";
	if (secs < 3600) return `${Math.round(secs / 60)}m ago`;
	if (secs < 86400) return `${Math.round(secs / 3600)}h ago`;
	return `${Math.round(secs / 86400)}d ago`;
}
