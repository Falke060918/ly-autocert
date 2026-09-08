import { Alert, AlertDescription } from "@/components/ui/alert";
import type { IssuanceWarning } from "@/lib/api";

export function IssuanceWarnings({ warnings, compact = false }: {
  warnings?: IssuanceWarning[];
  compact?: boolean;
}) {
  if (!warnings?.length) return null;
  const title = warnings.every((w) => w.code.startsWith("ca_chain_")) ? "CA 체인 경고" : "발급 검증 경고";
  if (compact) return (
    <span className="mt-1 block text-xs font-medium text-amber-700 dark:text-amber-400"
      title={warnings.map((w) => w.message).join("\n")}>
      {title}
    </span>
  );
  return (
    <Alert className="border-amber-300 bg-amber-50 text-amber-900 dark:border-amber-800 dark:bg-amber-950 dark:text-amber-200">
      <AlertDescription className="text-inherit">
        <span className="font-semibold">{title}</span>
        {warnings.map((w, i) => <p key={`${w.code}-${i}`}>{w.message}</p>)}
      </AlertDescription>
    </Alert>
  );
}
