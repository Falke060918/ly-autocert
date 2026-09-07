"use client";
import { useCallback, useEffect, useState, type ReactNode } from "react";
import { apiFetch } from "@/lib/api";
export function useResource<T>(path: string | null, poll = 0) {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState("");
  const reload = useCallback(async () => {
    if (!path) return;
    try {
      const v = await apiFetch<T>("/api/v1" + path);
      setData(v);
      setError("");
    } catch (e) {
      setError(e instanceof Error ? e.message : "조회 실패");
    }
  }, [path]);
  useEffect(() => {
    let disposed = false;
    const load = async () => {
      if (!path) return;
      try {
        const v = await apiFetch<T>("/api/v1" + path);
        if (!disposed) {
          setData(v);
          setError("");
        }
      } catch (e) {
        if (!disposed) setError(e instanceof Error ? e.message : "조회 실패");
      }
    };
    void load();
    const timer = poll ? setInterval(load, poll) : undefined;
    return () => {
      disposed = true;
      clearInterval(timer);
    };
  }, [path, poll]);
  return { data, error, reload };
}
export function useAction() {
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const run = async (fn: () => Promise<void>) => {
    if (busy) return;
    setBusy(true);
    setMessage("");
    try {
      await fn();
    } catch (e) {
      setMessage(e instanceof Error ? e.message : "요청 실패");
    } finally {
      setBusy(false);
    }
  };
  return { busy, message, setMessage, run };
}
export function Field({
  label,
  children,
}: {
  label: string;
  children: ReactNode;
}) {
  return (
    <label className="grid gap-1 text-sm">
      <span className="text-muted-foreground">{label}</span>
      {children}
    </label>
  );
}
export const inputClass =
  "w-full rounded-md border bg-background px-3 py-2 text-sm";
export function Notice({ text }: { text: string }) {
  return text ? (
    <p role="status" className="rounded-md border p-3 text-sm break-words">
      {text}
    </p>
  ) : null;
}
export function Panel({
  title,
  children,
}: {
  title: string;
  children: ReactNode;
}) {
  return (
    <section className="space-y-4 rounded-xl border bg-card p-5">
      <h2 className="font-semibold">{title}</h2>
      {children}
    </section>
  );
}
