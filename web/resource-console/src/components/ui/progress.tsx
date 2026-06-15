import * as React from "react";
import { cn } from "../../lib/utils";

export function Progress({ value, className }: { value: number; className?: string }) {
  const width = Math.max(0, Math.min(100, value));
  return (
    <div className={cn("h-2 overflow-hidden rounded-full bg-muted", className)} role="progressbar" aria-valuenow={width}>
      <div className="h-full rounded-full bg-emerald-600 transition-all" style={{ width: `${width}%` }} />
    </div>
  );
}
