const paths = {
  servers: "M4 5h16v5H4zM4 14h16v5H4zM7.5 7.5h.01M7.5 16.5h.01",
  store: "M4 10v9a1 1 0 0 0 1 1h14a1 1 0 0 0 1-1v-9M3 6.5 5 4h14l2 2.5v1.5a3 3 0 0 1-6 0 3 3 0 0 1-6 0 3 3 0 0 1-6 0zM10 20v-5h4v5",
  members: "M16 19v-1a4 4 0 0 0-4-4H7a4 4 0 0 0-4 4v1M9.5 10a3 3 0 1 0 0-6 3 3 0 0 0 0 6M21 19v-1a4 4 0 0 0-3-3.85M15.5 4.15a3 3 0 0 1 0 5.7",
  key: "M15 9a3 3 0 1 0 0-.01M12.9 11.1 4 20m3-3 2 2m-.5-4.5 2 2",
  audit: "M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8zM14 3v5h5M9 13h6M9 17h4",
  chat: "M20 12a8 8 0 0 1-11.6 7.1L4 20l.9-4.4A8 8 0 1 1 20 12",
  logout: "M15 4h3a2 2 0 0 1 2 2v12a2 2 0 0 1-2 2h-3M10 17l-5-5 5-5M5 12h11",
  plus: "M12 5v14M5 12h14",
  refresh: "M20 11a8 8 0 0 0-14.3-4.4L4 8.5M4 4v4.5h4.5M4 13a8 8 0 0 0 14.3 4.4L20 15.5M20 20v-4.5h-4.5",
  alert: "M12 8v4.5M12 16h.01M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18",
  check: "M5 12.5 10 17 19 7",
  send: "M5 12h13M12 5l7 7-7 7",
  stop: "M7 7h10v10H7z",
  chevron: "M9 6l6 6-6 6",
  tool: "M14.7 6.3a4 4 0 0 0 5 5L14 17l-1 1a2.8 2.8 0 0 1-4-4l1-1 5.7-5.7M14.7 6.3 9 12",
  sparkle: "M12 3v4M12 17v4M3 12h4M17 12h4M6 6l2.5 2.5M15.5 15.5 18 18M6 18l2.5-2.5M15.5 8.5 18 6",
  external: "M14 4h6v6M20 4l-9 9M18 14v5a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V7a1 1 0 0 1 1-1h5",
  lock: "M6 11h12v9H6zM8.5 11V8a3.5 3.5 0 0 1 7 0v3",
  eye: "M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7S2 12 2 12zM12 9a3 3 0 1 0 0 6 3 3 0 0 0 0-6",
  eyeOff:
    "M3 3l18 18M10.6 5.1A10.4 10.4 0 0 1 12 5c6.5 0 10 7 10 7a17 17 0 0 1-3.2 4.1M6.6 6.6C3.9 8.4 2 12 2 12s3.5 7 10 7a9.7 9.7 0 0 0 5.4-1.6M9.9 9.9a3 3 0 0 0 4.2 4.2",
} as const;

export type IconName = keyof typeof paths;

export function Icon({ name, size = 16 }: { name: IconName; size?: number }) {
  return (
    <svg
      className="icon"
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={1.8}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
    >
      <path d={paths[name]} />
    </svg>
  );
}

export function BrandMark({ size = 28 }: { size?: number }) {
  return (
    <svg className="brand-mark" width={size} height={size} viewBox="0 0 32 32" aria-hidden="true" focusable="false">
      <rect width="32" height="32" rx="8" fill="var(--brand-bg)" />
      <path d="M8 21.5c2.7-1.6 5.3-1.6 8 0s5.3 1.6 8 0" fill="none" stroke="var(--brand-fg)" strokeWidth="2" strokeLinecap="round" opacity="0.55" />
      <path d="M8 16.5 12 10l4 5.5 4-5.5 4 6.5" fill="none" stroke="var(--brand-fg)" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}
