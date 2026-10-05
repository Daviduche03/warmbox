/** Comma-separated host list <-> array, shared by the create form and the policy editor. */
export function parseList(s: string): string[] {
	return s
		.split(",")
		.map((x) => x.trim())
		.filter(Boolean);
}

export function joinList(a?: string[]): string {
	return (a ?? []).join(", ");
}
