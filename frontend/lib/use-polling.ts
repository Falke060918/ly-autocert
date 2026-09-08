"use client";

import { useCallback, useEffect, useRef, type DependencyList } from "react";

export const STATUS_POLL_MS = 5000;

/** Poll after completion, never overlap requests, and ignore obsolete responses.
 * Hidden/offline pages pause; returning to the page refreshes immediately.
 * Callers list every captured query input in deps, just as for useEffect.
 */
export function usePolling(
  load: (current: () => boolean) => Promise<unknown>,
  deps: DependencyList,
  interval = STATUS_POLL_MS,
) {
  const refreshRef = useRef<(() => Promise<void>) | null>(null);
  useEffect(() => {
    let disposed = false;
    let pending: Promise<void> | null = null;
    let again = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const available = () => document.visibilityState !== "hidden" && navigator.onLine;
    const run = (): Promise<void> => {
      clearTimeout(timer);
      if (disposed || !available()) return Promise.resolve();
      if (pending) {
        again = true;
        return pending;
      }
      pending = Promise.resolve().then(async () => { await load(() => !disposed); }).catch(() => {
        // Loaders own their visible error state. A transient error must not stop polling.
      }).finally(() => {
        pending = null;
        if (disposed || !available()) return;
        if (again) {
          again = false;
          void run();
        } else if (interval > 0) {
          timer = setTimeout(() => void run(), interval);
        }
      });
      return pending;
    };
    refreshRef.current = run;
    const resume = () => {
      if (available()) void run();
      else clearTimeout(timer);
    };
    void run();
    // Static configuration resources (interval=0) refresh only on demand.
    if (interval > 0) {
      document.addEventListener("visibilitychange", resume);
      window.addEventListener("focus", resume);
      window.addEventListener("online", resume);
      window.addEventListener("offline", resume);
    }
    return () => {
      disposed = true;
      clearTimeout(timer);
      refreshRef.current = null;
      document.removeEventListener("visibilitychange", resume);
      window.removeEventListener("focus", resume);
      window.removeEventListener("online", resume);
      window.removeEventListener("offline", resume);
    };
    // The explicit dependency list is the query identity, like useEffect's API.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...deps, interval]);
  return useCallback(async () => { await refreshRef.current?.(); }, []);
}
