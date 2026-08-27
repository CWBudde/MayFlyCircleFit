// SkeletonBar is one grey placeholder line. The islands used to fall back to a
// bare sentence while their payload was in flight, which left the page a blank
// sheet that jumped once the content arrived; a skeleton keeps the shape the
// reader is about to get.
//
// It animates with the layout's own `pulse` keyframes rather than one of its
// own, which also means the layout's prefers-reduced-motion rule already stops
// it. It is aria-hidden: the sentence beside it is what a screen reader needs,
// and a run of empty boxes is noise.
export function SkeletonBar({ width, height = "1rem" }: { width: string; height?: string }) {
	return (
		<div
			aria-hidden="true"
			style={{
				width,
				height,
				borderRadius: "0.25rem",
				backgroundColor: "var(--border-color)",
				animation: "pulse 1.6s ease-in-out infinite",
			}}
		/>
	);
}
