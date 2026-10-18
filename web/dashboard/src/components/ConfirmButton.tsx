import { useEffect, useRef, useState } from "react";

interface Props {
  label: string;
  confirmLabel: string;
  onConfirm: () => void;
  disabled?: boolean;
  busy?: boolean;
}

export function ConfirmButton({ label, confirmLabel, onConfirm, disabled = false, busy = false }: Props) {
  const [armed, setArmed] = useState(false);
  const confirmRef = useRef<HTMLButtonElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const moveFocus = useRef(false);

  useEffect(() => {
    if (!moveFocus.current) return;
    moveFocus.current = false;
    // The button that had focus unmounts on each toggle, so hand focus to its replacement.
    (armed ? confirmRef : triggerRef).current?.focus();
  }, [armed]);

  const toggle = (next: boolean) => {
    moveFocus.current = true;
    setArmed(next);
  };

  if (!armed) {
    return (
      <button
        ref={triggerRef}
        type="button"
        className="button button-danger-quiet"
        disabled={disabled || busy}
        onClick={() => {
          toggle(true);
        }}
      >
        {busy ? "Working…" : label}
      </button>
    );
  }
  return (
    <span className="confirm">
      <button
        ref={confirmRef}
        type="button"
        className="button button-danger"
        onClick={() => {
          toggle(false);
          onConfirm();
        }}
      >
        {confirmLabel}
      </button>
      <button
        type="button"
        className="button button-quiet"
        onClick={() => {
          toggle(false);
        }}
      >
        Cancel
      </button>
    </span>
  );
}
