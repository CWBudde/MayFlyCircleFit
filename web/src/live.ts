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

// LiveConnectionState distinguishes "has not opened yet" from "opened and then
// dropped". A boolean cannot: it starts false, so every island painted
// "reconnecting" on first paint even on a perfectly healthy page, and the one
// state a reader has to act on looked identical to the one they never should.
export type LiveConnectionState = "connecting" | "connected" | "reconnecting";

type ConnectionNotice =
	| { kind: "event"; event: UIEvent }
	| { kind: "open" }
	| { kind: "error"; state: Exclude<LiveConnectionState, "connected"> }
	| { kind: "gap" };

type Listener = (notice: ConnectionNotice) => void;

const RECONCILE_INTERVAL_MS = 30_000;
const MIN_REFRESH_INTERVAL_MS = 1_000;

// LiveEventBus owns one EventSource for every island mounted by the bundle.
// EventSource already implements reconnect/backoff; onopen tells resources to
// reconcile instead of replacing that behavior with reload timers.
class LiveEventBus {
	private source: EventSource | null = null;
	private listeners = new Set<Listener>();
	private lastSequence = 0;
	private state: LiveConnectionState = "connecting";
	// EventSource reports a failed first connect and a dropped stream through
	// the same onerror, so the state needs to know which one happened.
	private hasOpened = false;

	subscribe(listener: Listener): () => void {
		this.listeners.add(listener);
		this.ensureConnected();
		// onopen fires once per connection, not once per listener, so an island
		// mounted after the stream came up would never learn it is connected and
		// would never take its first authoritative fetch. Replay the settled
		// state to the newcomer instead.
		if (this.state !== "connecting") {
			listener(this.state === "connected" ? { kind: "open" } : { kind: "error", state: this.state });
		}
		return () => {
			this.listeners.delete(listener);
			if (this.listeners.size === 0 && this.source) {
				this.source.close();
				this.source = null;
				this.lastSequence = 0;
				this.state = "connecting";
				this.hasOpened = false;
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
		source.onopen = () => {
			this.state = "connected";
			this.hasOpened = true;
			this.emit({ kind: "open" });
		};
		source.onerror = () => {
			// A stream that has never opened is still connecting. Calling that
			// "reconnecting" would announce the loss of something the reader
			// never had, which is the first-paint ambiguity the three-state
			// type exists to remove.
			this.state = this.hasOpened ? "reconnecting" : "connecting";
			this.emit({ kind: "error", state: this.state });
		};
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
	load: (signal: AbortSignal, current: T) => Promise<T>;
	reduce: (current: T, event: UIEvent) => ReduceResult<T>;
};

// useLiveResource serializes authoritative fetches and queues stream events
// while a fetch is in flight. A response therefore cannot erase a newer event:
// the queued events are replayed on top before the state is committed.
export function useLiveResource<T>({ initial, load, reduce }: LiveResourceOptions<T>) {
	const [value, setValue] = useState(initial);
	const [status, setStatus] = useState<LiveConnectionState>("connecting");
	const [error, setError] = useState<string | null>(null);
	const valueRef = useRef(initial);
	const mountedRef = useRef(false);
	const inFlightRef = useRef(false);
	const pendingRef = useRef(false);
	const lastRefreshStartedRef = useRef(0);
	const refreshTimerRef = useRef<number | null>(null);
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
		const delay = lastRefreshStartedRef.current + MIN_REFRESH_INTERVAL_MS - Date.now();
		if (delay > 0) {
			pendingRef.current = true;
			if (refreshTimerRef.current === null) {
				refreshTimerRef.current = window.setTimeout(() => {
					refreshTimerRef.current = null;
					pendingRef.current = false;
					if (mountedRef.current) void refresh();
				}, delay);
			}
			return;
		}
		inFlightRef.current = true;
		lastRefreshStartedRef.current = Date.now();
		pendingRef.current = false;
		queuedRef.current = [];
		const controller = new AbortController();
		controllerRef.current = controller;
		try {
			let next = (await loadRef.current(controller.signal, valueRef.current)) as T;
			let needsRefresh = false;
			// Drain the queue instead of iterating it: the stream keeps appending
			// while the fetch is in flight, so the replay must not race with those
			// pushes. The loop ends only once nothing is left, and no event can
			// arrive between that check and the reset of inFlightRef below.
			while (queuedRef.current.length > 0) {
				const replay = queuedRef.current;
				queuedRef.current = [];
				for (const event of replay) {
					const result = reduceRef.current(next, event);
					next = result.value;
					needsRefresh ||= result.refresh === true;
				}
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
					setStatus("connected");
					void refresh();
					break;
				case "error":
					// The bus decides which of the two failure states this is:
					// "reconnecting" only once the stream has actually opened in this
					// bus's lifetime, "connecting" while the first connect is still
					// being retried.
					setStatus(notice.state);
					break;
				case "gap":
					void refresh();
					break;
				case "event": {
					if (inFlightRef.current) {
						queuedRef.current.push(notice.event);
						return;
					}
					// Reducers also run outside React during replay and some of them
					// have effects, so one runs here instead of inside a state updater
					// React is free to invoke more than once.
					const result = reduceRef.current(valueRef.current, notice.event);
					commit(result.value);
					if (result.refresh) void refresh();
					break;
				}
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
			if (refreshTimerRef.current !== null) window.clearTimeout(refreshTimerRef.current);
			window.clearInterval(interval);
			document.removeEventListener("visibilitychange", reconcileVisible);
			window.removeEventListener("focus", reconcileVisible);
			unsubscribe();
		};
	}, [commit, refresh]);

	const update = useCallback((updater: (current: T) => T) => {
		commit(updater(valueRef.current));
	}, [commit]);

	return { value, status, error, refresh, update };
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
