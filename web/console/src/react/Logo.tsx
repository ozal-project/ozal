import { useId } from "react";

type LogoProps = {
  size?: number;
  animated?: boolean;
  entrance?: boolean;
  className?: string;
};

export function Logo({ size = 28, animated = false, entrance = false, className = "" }: LogoProps) {
  const id = `ozal-${useId().replace(/[^a-zA-Z0-9_-]/g, "")}`;
  const doorway = "M10.4 30V14.8A5.6 5.6 0 0 1 21.6 14.8V30Z";
  const arch = "M5 30V14.5A11 11 0 0 1 27 14.5V30H21.6V14.8A5.6 5.6 0 0 0 10.4 14.8V30Z";

  return <svg width={size} height={size} viewBox="0 0 32 32" className={["logo", animated && "logo-live", entrance && "logo-entrance", className].filter(Boolean).join(" ")} aria-hidden="true">
    <defs>
      <clipPath id={`${id}-door`}><path d={doorway} /></clipPath>
      <linearGradient id={`${id}-beam`} x1="0" x2="1" y1="0" y2="0">
        <stop offset="0" stopColor="var(--primary)" stopOpacity="0" />
        <stop offset="0.5" stopColor="var(--primary)" stopOpacity="0.8" />
        <stop offset="1" stopColor="var(--primary)" stopOpacity="0" />
      </linearGradient>
    </defs>
    <g className="logo-door" clipPath={`url(#${id}-door)`}>
      <path className="logo-light" d={doorway} fill="var(--signal)" />
      <rect className="logo-beam" x="4" y="8" width="5" height="24" fill={`url(#${id}-beam)`} opacity="0" />
      <path className="logo-leaf" d="M10.4 30V14.8A5.6 5.6 0 0 1 16 9.2V30Z" fill="var(--depth)" />
    </g>
    <path className="logo-arch" d={arch} fill="var(--primary)" />
    {entrance && <path className="logo-trace" d={arch} pathLength="100" strokeDasharray="100" fill="none" stroke="var(--primary)" strokeWidth="0.6" strokeLinejoin="round" />}
  </svg>;
}
