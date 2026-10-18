import { api } from "./endpoints";
import type { ChatEvent, ChatMessage } from "./types";

type Fields = Record<string, unknown>;

function str(fields: Fields, key: string): string {
  const value = fields[key];
  return typeof value === "string" ? value : "";
}

function parseEvent(name: string, data: string): ChatEvent | null {
  let parsed: unknown;
  try {
    parsed = JSON.parse(data);
  } catch {
    return null;
  }
  if (typeof parsed !== "object" || parsed === null) return null;
  const fields = parsed as Fields;
  switch (name) {
    case "tool_call":
      return { type: "tool_call", id: str(fields, "id"), name: str(fields, "name"), arguments: fields.arguments ?? {} };
    case "tool_result":
      return {
        type: "tool_result",
        id: str(fields, "id"),
        name: str(fields, "name"),
        is_error: fields.is_error === true,
        content: str(fields, "content"),
      };
    case "answer":
      return { type: "answer", text: str(fields, "text") };
    case "error":
      return { type: "error", message: str(fields, "message") };
    case "done":
      return { type: "done" };
    default:
      return null;
  }
}

function parseBlock(block: string): ChatEvent | null {
  let name = "message";
  const data: string[] = [];
  for (const line of block.split("\n")) {
    if (line.startsWith(":")) continue;
    const colon = line.indexOf(":");
    const field = colon === -1 ? line : line.slice(0, colon);
    let value = colon === -1 ? "" : line.slice(colon + 1);
    if (value.startsWith(" ")) value = value.slice(1);
    if (field === "event") name = value;
    else if (field === "data") data.push(value);
  }
  return data.length === 0 ? null : parseEvent(name, data.join("\n"));
}

export async function streamChat(
  messages: ChatMessage[],
  signal: AbortSignal,
  onEvent: (event: ChatEvent) => void,
): Promise<void> {
  const res = await api.chat(messages, signal);
  if (!res.body) throw new Error("The chat response had no body.");
  const reader = res.body.pipeThrough(new TextDecoderStream()).getReader();
  let buffer = "";
  for (;;) {
    const { value, done } = await reader.read();
    if (done) break;
    buffer += value;
    let end = buffer.indexOf("\n\n");
    while (end !== -1) {
      const event = parseBlock(buffer.slice(0, end));
      buffer = buffer.slice(end + 2);
      if (event) onEvent(event);
      end = buffer.indexOf("\n\n");
    }
  }
  const tail = parseBlock(buffer.trim());
  if (tail) onEvent(tail);
}
