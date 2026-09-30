import { useCallback, useState } from "react";

/**
 * How long a pending flag stays up even when the request returns instantly.
 * The daemon answers most mutations in a few milliseconds (a warm pool hands
 * over an already-booted VM), which otherwise reads as the button doing
 * nothing at all.
 */
const MIN_VISIBLE_MS = 450;

/** Runs an async action and reports it as pending for at least MIN_VISIBLE_MS. */
export function usePending() {
	const [pending, setPending] = useState(false);

	const run = useCallback(async (action: () => Promise<void>) => {
		setPending(true);
		const startedAt = Date.now();
		try {
			await action();
		} finally {
			const elapsed = Date.now() - startedAt;
			if (elapsed < MIN_VISIBLE_MS) {
				await new Promise((resolve) =>
					setTimeout(resolve, MIN_VISIBLE_MS - elapsed)
				);
			}
			setPending(false);
		}
	}, []);

	return { pending, run };
}
