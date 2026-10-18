export type Tone = "green" | "amber" | "red" | "grey";

export function Badge({ tone, children }: { tone: Tone; children: string }) {
  return <span className={`badge badge-${tone}`}>{children}</span>;
}
