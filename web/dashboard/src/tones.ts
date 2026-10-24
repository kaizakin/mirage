import type { Outcome, ServerStatus } from "./api/types";
import type { Tone } from "./components/Badge";

export const statusTone: Record<ServerStatus, Tone> = {
  connected: "green",
  connecting: "amber",
  unreachable: "red",
  disabled: "grey",
};

export const outcomeStyle: Record<Outcome, { label: string; tone: Tone }> = {
  ok: { label: "Success", tone: "green" },
  tool_error: { label: "Tool error", tone: "red" },
  timeout: { label: "Timed out", tone: "amber" },
  unreachable: { label: "Unreachable", tone: "red" },
  canceled: { label: "Canceled", tone: "grey" },
  rate_limited: { label: "Rate limited", tone: "amber" },
  denied: { label: "Denied", tone: "grey" },
};
