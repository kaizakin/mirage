export type Tone = "green" | "amber" | "red" | "grey";

/** busy swaps the dot for a spinner, for states that are still changing. */
export function Badge({ tone, busy = false, children }: { tone: Tone; busy?: boolean; children: string }) {
  return (
    <span className={`badge badge-${tone}`}>
      <span className={busy ? "spinner" : "dot"} aria-hidden="true" />
      {children}
    </span>
  );
}
