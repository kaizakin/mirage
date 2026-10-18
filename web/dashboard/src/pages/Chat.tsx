import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState, type KeyboardEvent } from "react";
import { streamChat } from "../api/chat";
import { errorMessage, isStatus } from "../api/client";
import { setSession } from "../session";
import { applyEvent, history, pendingTurn, type AssistantTurn, type ToolStep, type Turn } from "./chatModel";

function StepCard({ step }: { step: ToolStep }) {
  const status = step.result === null ? "running" : step.result.isError ? "error" : "ok";
  return (
    <details className={`step step-${status}`}>
      <summary>
        {status === "running" && <span className="spinner" aria-hidden="true" />}
        <span>
          {status === "running" ? "calling" : "called"} <code>{step.name}</code>
          {status !== "running" && <span className="step-outcome"> → {status}</span>}
        </span>
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
      {turn.steps.length > 0 && (
        <div className="steps">
          {turn.steps.map((step) => (
            <StepCard key={step.id} step={step} />
          ))}
        </div>
      )}
      {turn.answer !== null && <div className="bubble">{turn.answer}</div>}
      {turn.error !== null && (
        <p className="alert alert-error" role="alert">
          {turn.error}
        </p>
      )}
      {turn.streaming && turn.answer === null && turn.steps.every((step) => step.result !== null) && (
        <p className="muted thinking">
          <span className="spinner" aria-hidden="true" />
          Thinking…
        </p>
      )}
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
          <button type="button" className="button button-quiet" disabled={streaming} onClick={() => { setTurns([]); }}>
            New conversation
          </button>
        )}
      </div>
      <div className="conversation" role="log" aria-live="polite" aria-busy={streaming}>
        {turns.length === 0 && <p className="muted empty-state">Try "list the five newest products".</p>}
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
          rows={2}
          placeholder={streaming ? "Waiting for the answer…" : "Message the assistant. Enter sends, Shift+Enter adds a line."}
          disabled={streaming}
          value={input}
          onChange={(e) => { setInput(e.target.value); }}
          onKeyDown={onKeyDown}
        />
        {streaming ? (
          <button type="button" className="button" onClick={() => abort.current?.abort()}>
            Stop
          </button>
        ) : (
          <button type="submit" className="button button-primary" disabled={!input.trim()}>
            Send
          </button>
        )}
      </form>
    </section>
  );
}
