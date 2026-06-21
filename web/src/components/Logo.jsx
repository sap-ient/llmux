import { useId } from "react";

// llmux mark — the canonical Vulos lightning bolt inside a routing tile, with
// many input ports → one output port (the multiplexer). Brand gradient
// (teal → purple) is fixed so the mark is identical in light and dark.
export function Mark({ size = 30 }) {
  const id = "vg-" + useId().replace(/:/g, "");
  return (
    <svg className="mark" width={size} height={size} viewBox="0 0 64 64" fill="none" aria-hidden="true">
      <defs>
        <linearGradient id={id} x1="12" y1="8" x2="54" y2="58" gradientUnits="userSpaceOnUse">
          <stop offset="0" stopColor="#19a3a6" />
          <stop offset="1" stopColor="#C96AFF" />
        </linearGradient>
      </defs>
      <rect x="6" y="6" width="52" height="52" rx="14" fill={`url(#${id})`} opacity="0.12" />
      <rect x="6" y="6" width="52" height="52" rx="14" fill="none" stroke={`url(#${id})`} strokeWidth="2.5" />
      <g fill={`url(#${id})`}>
        <circle cx="6" cy="24" r="2.6" /><circle cx="6" cy="32" r="2.6" /><circle cx="6" cy="40" r="2.6" />
        <circle cx="58" cy="32" r="2.6" />
      </g>
      <g transform="translate(20.5 16) scale(0.62)" fill={`url(#${id})`}>
        <path d="M25.946 44.938c-.664.845-2.021.375-2.021-.698V33.937a2.26 2.26 0 0 0-2.262-2.262H10.287c-.92 0-1.456-1.04-.92-1.788l7.48-10.471c1.07-1.497 0-3.578-1.842-3.578H1.237c-.92 0-1.456-1.04-.92-1.788L10.013.474c.214-.297.556-.474.92-.474h28.894c.92 0 1.456 1.04.92 1.788l-7.48 10.471c-1.07 1.498 0 3.579 1.842 3.579h11.377c.943 0 1.473 1.088.89 1.83L25.947 44.94z" />
      </g>
    </svg>
  );
}

export function Logo() {
  return (
    <span className="logo">
      <span className="logo-mark"><Mark size={30} /></span>
      <span className="word">llmu<span className="x">x</span></span>
    </span>
  );
}
