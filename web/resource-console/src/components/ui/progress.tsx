import * as React from "react";
import { cn } from "../../lib/utils";

export function Progress({
  value,
  className,
  indicatorClassName
}: {
  value: number;
  className?: string;
  indicatorClassName?: string;
}) {
  const width = Math.max(0, Math.min(100, value));
  return (
    <div className={cn("h-2 overflow-hidden rounded-full bg-muted", className)} role="progressbar" aria-valuenow={width}>
      <div className={cn("h-full rounded-full bg-emerald-600 transition-all", indicatorClassName)} style={{ width: `${width}%` }} />
    </div>
  );
}
