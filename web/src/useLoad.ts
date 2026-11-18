import { useCallback, useEffect, useState, type DependencyList } from "react";
import { errorMessage } from "./api/client";

// useLoad runs load when the component mounts (and when deps change), cancelling the
// request if the component goes away first. setData lets the page update what it
// loaded after a change, without reloading.
export function useLoad<T>(load: (signal: AbortSignal) => Promise<T>, deps: DependencyList) {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    const ctl = new AbortController();
    setError(null);
    load(ctl.signal)
      .then((d) => {
        if (!ctl.signal.aborted) {
          setData(d);
        }
      })
      .catch((err: unknown) => {
        if (!ctl.signal.aborted) {
          setError(errorMessage(err));
        }
      });
    return () => ctl.abort();
    // load is a new function on every render, so it isn't a dependency itself: the
    // caller's deps say when the data really needs loading again.
  }, [...deps, attempt]);

  const reload = useCallback(() => setAttempt((n) => n + 1), []);
  return { data, setData, error, reload };
}

// formatDate shows a timestamp as a short local date, e.g. "18 Nov 2026".
export function formatDate(iso: string): string {
  return new Date(iso).toLocaleDateString(undefined, { day: "numeric", month: "short", year: "numeric" });
}
