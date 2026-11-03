// What the console's account menu says about the signed-in administrator:
// initials for the avatar, a one-line summary of the tools, and a line per
// passkey. Pure, so the wording is tested without a browser.
import type { OwnPasskey } from "./passkey.ts";

export const allTools = ["administrators", "applications", "connectors", "people", "publishing", "sites", "updates", "updates.install"] as const;

export function initials(name: string): string {
  const words = name.trim().split(/\s+/).filter(Boolean);
  if (!words.length) return "?";
  const letters = words.length === 1 ? words[0].slice(0, 1) : words[0].slice(0, 1) + words[words.length - 1].slice(0, 1);
  return letters.toUpperCase();
}

export function toolSummary(tools: readonly string[]): string {
  const held = new Set(tools);
  const missing = allTools.filter((tool) => !held.has(tool));
  if (!tools.length) return "no tools";
  if (!missing.length) return "every tool";
  if (missing.length === 1 && missing[0] === "updates.install") return "every tool except installing updates";
  return `${allTools.length - missing.length} of ${allTools.length} tools`;
}

export function passkeyLine(p: OwnPasskey, locale?: string): { name: string; detail: string; current: boolean } {
  const day = (iso: string) => new Date(iso).toLocaleDateString(locale, { month: "short", day: "numeric" });
  // The current one carries a "This sign-in" mark beside it; its line does not repeat it.
  const used = p.current ? "" : p.last_used_at ? ` · last used ${day(p.last_used_at)}` : " · not used yet";
  return { name: p.label?.trim() || "Passkey", detail: `added ${day(p.created_at)}${used}`, current: Boolean(p.current) };
}
