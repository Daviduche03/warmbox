import { useState } from "react";

export function useCopy(): [boolean, (text: string) => Promise<void>] {
	const [copied, setCopied] = useState(false);
	async function copy(text: string) {
		if (!text) return;
		try {
			await navigator.clipboard.writeText(text);
			setCopied(true);
			window.setTimeout(() => setCopied(false), 1600);
		} catch {
			/* clipboard unavailable */
		}
	}
	return [copied, copy];
}
