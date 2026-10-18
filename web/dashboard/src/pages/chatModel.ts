import type { ChatEvent, ChatMessage } from "../api/types";

export interface ToolStep {
  id: string;
  name: string;
  arguments: unknown;
  result: { isError: boolean; content: string } | null;
}

export interface UserTurn {
  role: "user";
  text: string;
}

export interface AssistantTurn {
  role: "assistant";
  steps: ToolStep[];
  answer: string | null;
  error: string | null;
  streaming: boolean;
}

export type Turn = UserTurn | AssistantTurn;

export const pendingTurn: AssistantTurn = { role: "assistant", steps: [], answer: null, error: null, streaming: true };

export function applyEvent(turn: AssistantTurn, event: ChatEvent): AssistantTurn {
  switch (event.type) {
    case "tool_call":
      return { ...turn, steps: [...turn.steps, { id: event.id, name: event.name, arguments: event.arguments, result: null }] };
    case "tool_result":
      return {
        ...turn,
        steps: turn.steps.map((step) =>
          step.id === event.id ? { ...step, result: { isError: event.is_error, content: event.content } } : step,
        ),
      };
    case "answer":
      return { ...turn, answer: (turn.answer ?? "") + event.text };
    case "error":
      return { ...turn, error: event.message };
    case "done":
      return { ...turn, streaming: false };
  }
}

export function history(turns: readonly Turn[]): ChatMessage[] {
  return turns.flatMap((turn): ChatMessage[] => {
    if (turn.role === "user") return [{ role: "user", content: turn.text }];
    return turn.answer ? [{ role: "assistant", content: turn.answer }] : [];
  });
}
