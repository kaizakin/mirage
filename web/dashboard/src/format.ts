const dateTime = new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" });
const precise = new Intl.DateTimeFormat(undefined, { dateStyle: "short", timeStyle: "medium" });

export function formatDate(iso: string): string {
  return dateTime.format(new Date(iso));
}

export function formatTimestamp(iso: string): string {
  return precise.format(new Date(iso));
}

export function formatDuration(ms: number): string {
  return ms < 1000 ? `${ms} ms` : `${(ms / 1000).toFixed(ms < 10_000 ? 2 : 1)} s`;
}
