import type { UsageWindow } from "../api";
import { cn } from "../lib/utils";

const windows: Array<{ value: UsageWindow; label: string }> = [
  { value: "24h", label: "24 小时" },
  { value: "7d", label: "7 天" },
  { value: "30d", label: "30 天" },
  { value: "all", label: "全部" }
];

export function UsageWindowControl({ value, onChange, ariaLabel = "统计时间范围" }: { value: UsageWindow; onChange: (value: UsageWindow) => void; ariaLabel?: string }) {
  return (
    <div className="inline-flex h-9 items-center rounded-md border bg-muted p-0.5" aria-label={ariaLabel}>
      {windows.map((item) => (
        <button
          key={item.value}
          type="button"
          onClick={() => onChange(item.value)}
          className={cn("h-8 rounded px-2.5 text-xs text-muted-foreground transition-colors", value === item.value && "bg-card font-medium text-foreground shadow-sm")}
        >
          {item.label}
        </button>
      ))}
    </div>
  );
}
