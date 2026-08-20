import { useCallback, useEffect, useRef, useState } from "react";

export type ProgressEvent = {
	jobId: string;
	state: string;
	iterations: number;
	evaluations: number;
	bestCost: number;
	bestRevision: number;
	candidateCost?: number;
	candidatePsnr?: number;
	candidatePsnrInfinite?: boolean;
	psnr?: number | null;
	psnrInfinite?: boolean;
	ssim?: number;
	cps: number;
	timestamp: string;
};

export type UIEvent = {
	sequence: number;
	type: "job.upsert" | "job.deleted" | "campaign.changed" | "sync";
	jobId?: string;
	campaignId?: string;
	source?: string;
	progress?: ProgressEvent;
	timestamp: string;
};

type ConnectionNotice =
	| { kind: "event"; event: UIEvent }
	| { kind: "open" }
	| { kind: "error" }
	| { kind: "gap" };

type Listener = (notice: ConnectionNotice) => void;

const RECONCILE_INTERVAL_MS = 30_000;

// LiveEventBus owns one EventSource for every island mounted by the bundle.
// EventSource already implements reconnect/backoff; onopen tells resources to
// reconcile instead of replacing that behavior with reload timers.
class LiveEventBus {
	private source: EventSource | null = null;
	private listeners = new Set<Listener>();
	private lastSequence = 0;

	subscribe(listener: Listener): () => void {
		this.listeners.add(listener);
		this.ensureConnected();
		return () => {
			this.listeners.delete(listener);
			if (this.listeners.size === 0 && this.source) {
				this.source.close();
				this.source = null;
				this.lastSequence = 0;
			}
		};
	}

	private emit(notice: ConnectionNotice): void {
		for (const listener of this.listeners) listener(notice);
	}

	private ensureConnected(): void {
		if (this.source) return;
		const source = new EventSource("/api/v1/events");
		this.source = source;
		source.onopen = () => this.emit({ kind: "open" });
		source.onerror = () => this.emit({ kind: "error" });
		source.onmessage = (message) => {
			let event: UIEvent;
			try {
				event = JSON.parse(message.data) as UIEvent;
			} catch (error) {
				console.error("Unable to parse live UI event", error);
				this.emit({ kind: "gap" });
				return;
			}
			if (!Number.isSafeInteger(event.sequence) || event.sequence < 0) {
				this.emit({ kind: "gap" });
				return;
			}

			if (event.type === "sync") {
				if (this.lastSequence !== 0 && event.sequence > this.lastSequence) {
					this.emit({ kind: "gap" });
				}
				this.lastSequence = Math.max(this.lastSequence, event.sequence);
				return;
			}
			if (event.sequence <= this.lastSequence) return;
			if (this.lastSequence !== 0 && event.sequence !== this.lastSequence + 1) {
				this.emit({ kind: "gap" });
			}
			this.lastSequence = event.sequence;
			this.emit({ kind: "event", event });
		};
	}
}

const liveEvents = new LiveEventBus();

export type ReduceResult<T> = { value: T; refresh?: boolean };

type LiveResourceOptions<T> = {
	initial: T;
	load: (signal: AbortSignal) => Promise<T>;
	reduce: (current: T, event: UIEvent) => ReduceResult<T>;
};

// useLiveResource serializes authoritative fetches and queues stream events
// while a fetch is in flight. A response therefore cannot erase a newer event:
// the queued events are replayed on top before the state is committed.
export function useLiveResource<T>({ initial, load, reduce }: LiveResourceOptions<T>) {
	const [value, setValue] = useState(initial);
	const [connected, setConnected] = useState(false);
	const [error, setError] = useState<string | null>(null);
	const valueRef = useRef(initial);
	const mountedRef = useRef(false);
	const inFlightRef = useRef(false);
	const pendingRef = useRef(false);
	const queuedRef = useRef<UIEvent[]>([]);
	const controllerRef = useRef<AbortController | null>(null);
	const loadRef = useRef(load);
	const reduceRef = useRef(reduce);
	loadRef.current = load;
	reduceRef.current = reduce;

	const commit = useCallback((next: T) => {
		valueRef.current = next;
		setValue(next);
	}, []);

	const refresh = useCallback(async () => {
		if (!mountedRef.current) return;
		if (inFlightRef.current) {
			pendingRef.current = true;
			return;
		}
		inFlightRef.current = true;
		pendingRef.current = false;
		queuedRef.current = [];
		const controller = new AbortController();
		controllerRef.current = controller;
		try {
			let next = (await loadRef.current(controller.signal)) as T;
			let needsRefresh = false;
			for (const event of queuedRef.current) {
				const result = reduceRef.current(next, event);
				next = result.value;
				needsRefresh ||= result.refresh === true;
			}
			if (!mountedRef.current || controller.signal.aborted) return;
			commit(next);
			setError(null);
			pendingRef.current ||= needsRefresh;
		} catch (reason) {
			if (!controller.signal.aborted && mountedRef.current) {
				setError(reason instanceof Error ? reason.message : "Unable to refresh live state");
			}
		} finally {
			if (controllerRef.current === controller) controllerRef.current = null;
			inFlightRef.current = false;
			if (pendingRef.current && mountedRef.current) void refresh();
		}
	}, [commit]);

	useEffect(() => {
		mountedRef.current = true;
		const unsubscribe = liveEvents.subscribe((notice) => {
			switch (notice.kind) {
				case "open":
					setConnected(true);
					void refresh();
					break;
				case "error":
					setConnected(false);
					break;
				case "gap":
					void refresh();
					break;
				case "event":
					if (inFlightRef.current) {
						queuedRef.current.push(notice.event);
						return;
					}
					setValue((current) => {
						const result = reduceRef.current(current, notice.event);
						valueRef.current = result.value;
						if (result.refresh) void refresh();
						return result.value;
					});
			}
		});
		const interval = window.setInterval(() => void refresh(), RECONCILE_INTERVAL_MS);
		const reconcileVisible = () => {
			if (document.visibilityState === "visible") void refresh();
		};
		document.addEventListener("visibilitychange", reconcileVisible);
		window.addEventListener("focus", reconcileVisible);
		return () => {
			mountedRef.current = false;
			controllerRef.current?.abort();
			window.clearInterval(interval);
			document.removeEventListener("visibilitychange", reconcileVisible);
			window.removeEventListener("focus", reconcileVisible);
			unsubscribe();
		};
	}, [refresh]);

	return { value, connected, error, refresh };
}

export async function fetchJSON<T>(url: string, signal?: AbortSignal): Promise<T> {
	const response = await fetch(url, {
		headers: { Accept: "application/json" },
		cache: "no-store",
		signal,
	});
	if (!response.ok) throw new Error(`refresh failed: ${response.status}`);
	return (await response.json()) as T;
}
