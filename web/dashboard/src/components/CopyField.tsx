import { useEffect, useId, useState } from "react";

export function CopyField({ label, value, multiline = false }: { label: string; value: string; multiline?: boolean }) {
  const id = useId();
  const [copied, setCopied] = useState<"idle" | "copied" | "failed">("idle");

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
      await navigator.clipboard.writeText(value);
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
          <textarea id={id} readOnly value={value} rows={3} onFocus={(e) => { e.currentTarget.select(); }} />
        ) : (
          <input id={id} readOnly value={value} onFocus={(e) => { e.currentTarget.select(); }} />
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
