import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState, type KeyboardEvent } from "react";
import { streamChat } from "../api/chat";
import { errorMessage, isStatus } from "../api/client";
import { Icon } from "../components/Icon";
import { Alert } from "../components/States";
import { setSession } from "../session";
import { applyEvent, history, pendingTurn, type AssistantTurn, type ToolStep, type Turn } from "./chatModel";
import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";

const suggestions = [
  "List the five newest products",
  "Show orders that are still processing",
  "What is 100 USD in INR?",
  "Which tools can you use right now?",
];

function StepCard({ step }: { step: ToolStep }) {
  const status = step.result === null ? "running" : step.result.isError ? "error" : "ok";
  return (
    <details className={`step step-${status}`}>
      <summary>
        {status === "running" ? <span className="spinner" aria-hidden="true" /> : <Icon name="tool" size={14} />}
        <span className="step-label">
          {status === "running" ? "Calling" : "Called"} <code>{step.name}</code>
        </span>
        {status !== "running" && <span className="step-outcome">{status}</span>}
        <Icon name="chevron" size={14} />
      </summary>
      <div className="step-body">
        <h4>Arguments</h4>
        <pre>{JSON.stringify(step.arguments, null, 2)}</pre>
        <h4>Result</h4>
        {step.result === null ? <p className="muted">Waiting for the tool…</p> : <pre>{step.result.content}</pre>}
      </div>
    </details>
  );
}

function AssistantMessage({ turn }: { turn: AssistantTurn }) {
  return (
    <div className="message message-assistant">
      <span className="message-avatar" aria-hidden="true">
        <Icon name="sparkle" size={14} />
      </span>
      <div className="message-body">
        {turn.steps.length > 0 && (
          <div className="steps">
            {turn.steps.map((step) => (
              <StepCard key={step.id} step={step} />
            ))}
          </div>
        )}
        {turn.answer !== null && (
          // The model answers in Markdown (tables for lists). react-markdown renders no raw HTML,
          // so model output cannot inject markup.
          <div className="bubble markdown">
            <Markdown remarkPlugins={[remarkGfm]}>{turn.answer}</Markdown>
          </div>
        )}
        {turn.error !== null && <Alert>{turn.error}</Alert>}
        {turn.streaming && turn.answer === null && turn.steps.every((step) => step.result !== null) && (
          <p className="muted thinking">
            <span className="spinner" aria-hidden="true" />
            Thinking…
          </p>
        )}
      </div>
    </div>
  );
}

export function ChatPage() {
  const client = useQueryClient();
  const [turns, setTurns] = useState<Turn[]>([]);
  const [input, setInput] = useState("");
  const [streaming, setStreaming] = useState(false);
  const abort = useRef<AbortController | null>(null);
  const end = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);

  useEffect(() => () => abort.current?.abort(), []);

  useEffect(() => {
    if (!streaming) inputRef.current?.focus();
  }, [streaming]);

  useEffect(() => {
    end.current?.scrollIntoView({ block: "end" });
  }, [turns]);

  const updateLast = (update: (turn: AssistantTurn) => AssistantTurn) => {
    setTurns((current) => {
      const last = current.at(-1);
      if (last?.role !== "assistant") return current;
      return [...current.slice(0, -1), update(last)];
    });
  };

  const send = async (text: string) => {
    const next: Turn[] = [...turns, { role: "user", text }];
    setTurns([...next, pendingTurn]);
    setInput("");
    setStreaming(true);
    const controller = new AbortController();
    abort.current = controller;
    try {
      await streamChat(history(next), controller.signal, (event) => {
        updateLast((turn) => applyEvent(turn, event));
      });
    } catch (error) {
      if (isStatus(error, 401)) {
        setSession(client, null);
        return;
      }
      const message = controller.signal.aborted ? "Stopped." : errorMessage(error);
      updateLast((turn) => ({ ...turn, error: turn.error ?? message }));
    } finally {
      updateLast((turn) => ({ ...turn, streaming: false }));
      setStreaming(false);
      abort.current = null;
    }
  };

  const submit = () => {
    const text = input.trim();
    if (!text || streaming) return;
    void send(text);
  };

  const onKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing) {
      event.preventDefault();
      submit();
    }
  };

  return (
    <section className="chat" aria-labelledby="chat-title">
      <div className="page-head">
        <div>
          <h1 id="chat-title">Chat</h1>
          <p className="muted">Ask the assistant to use the gateway's tools on your behalf. Calls are audited as you.</p>
        </div>
        {turns.length > 0 && (
          <button type="button" className="button" disabled={streaming} onClick={() => { setTurns([]); }}>
            <Icon name="plus" />
            New conversation
          </button>
        )}
      </div>
      <div className="conversation" role="log" aria-live="polite" aria-busy={streaming}>
        {turns.length === 0 && (
          <div className="chat-empty">
            <span className="empty-icon">
              <Icon name="sparkle" size={20} />
            </span>
            <h2>What should the gateway do?</h2>
            <p className="muted">The assistant picks tools from the servers you can reach. Try one of these:</p>
            <ul className="suggestions">
              {suggestions.map((prompt) => (
                <li key={prompt}>
                  <button
                    type="button"
                    className="suggestion"
                    onClick={() => {
                      setInput(prompt);
                      inputRef.current?.focus();
                    }}
                  >
                    {prompt}
                  </button>
                </li>
              ))}
            </ul>
          </div>
        )}
        {turns.map((turn, index) =>
          turn.role === "user" ? (
            <div key={index} className="message message-user">
              <div className="bubble">{turn.text}</div>
            </div>
          ) : (
            <AssistantMessage key={index} turn={turn} />
          ),
        )}
        <div ref={end} />
      </div>
      <form
        className="composer"
        onSubmit={(event) => {
          event.preventDefault();
          submit();
        }}
      >
        <label htmlFor="chat-input" className="visually-hidden">
          Message
        </label>
        <textarea
          id="chat-input"
          ref={inputRef}
          rows={1}
          placeholder={streaming ? "Waiting for the answer…" : "Message the assistant. Enter sends, Shift+Enter adds a line."}
          disabled={streaming}
          value={input}
          onChange={(e) => { setInput(e.target.value); }}
          onKeyDown={onKeyDown}
        />
        {streaming ? (
          <button type="button" className="button composer-button" onClick={() => abort.current?.abort()}>
            <Icon name="stop" />
            Stop
          </button>
        ) : (
          <button type="submit" className="button button-primary composer-button" disabled={!input.trim()}>
            Send
            <Icon name="send" />
          </button>
        )}
      </form>
    </section>
  );
}
