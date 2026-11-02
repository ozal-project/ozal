import { Logo } from "./Logo";

type WordmarkProps = {
  sub?: string;
  animated?: boolean;
};

export function Wordmark({ sub = "console", animated = false }: WordmarkProps) {
  return <span className="flex items-center gap-2.5">
    <Logo size={30} animated={animated} />
    <span className="leading-none">
      <span className="block text-xl font-medium tracking-tight">ozal</span>
      <span className="text-muted-foreground mt-1 block text-xs">{sub}</span>
    </span>
  </span>;
}
