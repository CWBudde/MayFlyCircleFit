import type { CSSProperties, ReactNode } from "react";
import type { LiveConnectionState } from "./live";

// The five islands each rendered this line as a plain <p>, so a stream that
// dropped while the reader was elsewhere on the page changed nothing they could
// perceive. One live region, declared once, announces it in every island.
//
// data-live-state carries the raw state alongside the prose. Browser specs used
// to pin the literal copy "Live updates: connected", which made any wording
// change a test failure; the attribute lets them assert the state instead.
const stateWording: Record<LiveConnectionState, string> = {
	connecting: "connecting…",
	connected: "connected",
	reconnecting: "reconnecting",
};

export function LiveStatus({
	state,
	error,
	children,
	style,
}: {
	state: LiveConnectionState;
	error?: string | null;
	// Leading text that belongs in the same sentence, such as the job list's
	// "Showing 3 of 3 · ". It sits inside the live region deliberately: those
	// counts change for the same reason the connection does.
	children?: ReactNode;
	style?: CSSProperties;
}) {
	return (
		<p
			role="status"
			aria-live="polite"
			data-live-state={state}
			style={{ color: "var(--text-muted)", fontSize: "0.875rem", ...style }}
		>
			{children}
			Live updates: {stateWording[state]}
			{error ? ` · ${error}` : ""}
		</p>
	);
}
