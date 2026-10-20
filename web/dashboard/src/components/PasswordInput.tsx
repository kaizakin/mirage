import { useState, type ComponentProps } from "react";
import { Icon } from "./Icon";

// PasswordInput is a password field with a button that shows or hides what was typed.
export function PasswordInput(props: Omit<ComponentProps<"input">, "type">) {
  const [visible, setVisible] = useState(false);
  return (
    <div className="password-input">
      <input {...props} type={visible ? "text" : "password"} />
      <button
        type="button"
        className="password-toggle"
        aria-label={visible ? "Hide password" : "Show password"}
        aria-controls={props.id}
        aria-pressed={visible}
        onClick={() => { setVisible((v) => !v); }}
      >
        <Icon name={visible ? "eyeOff" : "eye"} />
      </button>
    </div>
  );
}
