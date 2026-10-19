import { useEffect, useId, useRef, useState } from "react";

export function CopyField({ label, value, multiline = false }: { label: string; value: string; multiline?: boolean }) {
  const id = useId();
  const [copied, setCopied] = useState<"idle" | "copied" | "failed">("idle");
  const field = useRef<HTMLInputElement & HTMLTextAreaElement>(null);

  useEffect(() => {
    if (copied === "idle") return;
    const timer = setTimeout(() => {
      setCopied("idle");
    }, 2000);
    return () => {
      clearTimeout(timer);
    };
  }, [copied]);

  const copy = async () => {
    try {
      // navigator.clipboard exists only in secure contexts, and the local cluster serves the
      // dashboard over plain HTTP. There the selected field is copied the older way.
      if (window.isSecureContext) {
        await navigator.clipboard.writeText(value);
      } else {
        field.current?.select();
        // eslint-disable-next-line @typescript-eslint/no-deprecated -- the only copy API outside secure contexts
        if (!document.execCommand("copy")) throw new Error("copy refused");
      }
      setCopied("copied");
    } catch {
      setCopied("failed");
    }
  };

  return (
    <div className="copy-field">
      <label htmlFor={id}>{label}</label>
      <div className="copy-row">
        {multiline ? (
          <textarea ref={field} id={id} readOnly value={value} rows={3} onFocus={(e) => { e.currentTarget.select(); }} />
        ) : (
          <input ref={field} id={id} readOnly value={value} onFocus={(e) => { e.currentTarget.select(); }} />
        )}
        <button type="button" className="button" onClick={() => void copy()}>
          Copy
        </button>
      </div>
      <span className="copy-status" role="status">
        {copied === "copied" ? "Copied to clipboard." : copied === "failed" ? "Copy failed; select the text and copy it." : ""}
      </span>
    </div>
  );
}
