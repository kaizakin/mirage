const dateTime = new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" });
const precise = new Intl.DateTimeFormat(undefined, { dateStyle: "short", timeStyle: "medium" });

export function formatDate(iso: string): string {
  return dateTime.format(new Date(iso));
}

export function formatTimestamp(iso: string): string {
  return precise.format(new Date(iso));
}

const relative = new Intl.RelativeTimeFormat(undefined, { numeric: "auto" });
const units: readonly [Intl.RelativeTimeFormatUnit, number][] = [
  ["day", 86_400],
  ["hour", 3600],
  ["minute", 60],
];

export function formatRelative(iso: string, now = Date.now()): string {
  const seconds = Math.round((new Date(iso).getTime() - now) / 1000);
  for (const [unit, size] of units) {
    if (Math.abs(seconds) >= size) return relative.format(Math.round(seconds / size), unit);
  }
  return "just now";
}

export function formatDuration(ms: number): string {
  return ms < 1000 ? `${ms} ms` : `${(ms / 1000).toFixed(ms < 10_000 ? 2 : 1)} s`;
}
