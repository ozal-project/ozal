// What the Overview page says, as pure functions over data the console already
// reads (status, live events). Wording lives here, not in the page, so it is
// tested and the page only lays it out. Nothing here decides access.
import type { AccessEvent, StatusResponse } from "./api";
import { friendlyHealth, whoActed } from "./wording.ts";

export type Attention = {
  kind: "route" | "certificate" | "audit";
  title: string;
  detail: string;
  // The error as the edge reported it, for whoever needs the exact words.
  raw?: string;
  action?: { label: string; page: "/routes" | "/sites"; host?: string };
};

export type Change = {
  time: string;
  type: string;
  title: string;
  detail: string;
  source?: string;
  tone: "good" | "bad" | "neutral";
  // Putting the previous version back is offered only for a deploy that went live.
  undo?: { site: string };
};

export type AccessSummary = {
  allowed: number;
  refused: number;
  rateLimited: number;
  loginsStarted: number;
  reasons: { code: string; text: string; count: number }[];
};

const plural = (n: number, one: string, many = `${one}s`) => `${n} ${n === 1 ? one : many}`;

export function headline(status: StatusResponse | null, needAttention: number): string {
  if (!status) return "Loading…";
  const sites = status.routes.filter((r) => r.is_static).length;
  const apps = status.routes.length - sites;
  const serving = [apps > 0 ? plural(apps, "app") : "", sites > 0 ? plural(sites, "site") : ""].filter(Boolean).join(" and ") || "nothing yet";
  const state = needAttention === 0 ? "Everything is answering." : needAttention === 1 ? "One thing needs you." : `${needAttention} things need you.`;
  return `Serving ${serving}. ${state}`;
}

export function attention(status: StatusResponse | null): Attention[] {
  if (!status) return [];
  const items: Attention[] = [];
  for (const r of status.routes) {
    if (r.state_class !== "unhealthy") continue;
    const since = r.last_transition ? ` since ${new Date(r.last_transition).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}` : "";
    items.push({
      kind: "route",
      title: `${r.host} is not answering`,
      detail: `Visitors see an error page${since ? ` (${since.trim()})` : ""}. The app behind it is down: ${friendlyHealth(r.last_error)}.`,
      raw: r.last_error || undefined,
      action: { label: "Inspect route", page: "/routes", host: r.host },
    });
  }
  const cert = status.certificate;
  if (cert && (!cert.valid || cert.last_error)) {
    items.push({
      kind: "certificate",
      title: `The certificate for ${cert.host} needs attention`,
      detail: cert.last_error ? `The last renewal attempt failed: ${cert.last_error}.` : "It is not valid right now.",
    });
  }
  const audit = status.audit;
  if (audit && (!audit.available || audit.failure_count > 0)) {
    items.push({
      kind: "audit",
      title: audit.available ? "The audit log failed to record some events" : "The audit log is not available",
      detail: audit.available ? `${plural(audit.failure_count, "write")} failed${audit.last_failure_at ? `, the last at ${new Date(audit.last_failure_at).toLocaleTimeString()}` : ""}.` : "Changes are not being recorded.",
    });
  }
  return items;
}

const refusalText: Record<string, string> = {
  authentication_required: "Not signed in",
  missing_required_group: "Not in the group the app needs",
  email_not_allowed: "Email address not allowed",
  domain_not_allowed: "Email domain not allowed",
  email_not_verified: "Email address not verified",
  reauthentication_required: "Needed to sign in again",
  device_bound_required: "Needed a device-bound passkey",
  context_rule_denied: "Blocked by a context rule",
};

export function accessSummary(events: AccessEvent[]): AccessSummary {
  const sum: AccessSummary = { allowed: 0, refused: 0, rateLimited: 0, loginsStarted: 0, reasons: [] };
  const byCode = new Map<string, number>();
  for (const e of events) {
    if (e.type === "access.allowed") sum.allowed++;
    else if (e.type === "access.rate_limited") sum.rateLimited++;
    else if (e.type === "login.started") sum.loginsStarted++;
    else if (e.type === "access.denied") {
      sum.refused++;
      const code = e.code || "unspecified";
      byCode.set(code, (byCode.get(code) ?? 0) + 1);
    }
  }
  sum.reasons = [...byCode].map(([code, count]) => ({ code, text: refusalText[code] ?? code, count })).sort((a, b) => b.count - a.count);
  return sum;
}

const isChange = (type: string) => !type.startsWith("access.") && !type.startsWith("login.") && !type.startsWith("origin.") && type !== "admin.mutation_requested";

function deploySource(e: AccessEvent, subjects?: Map<string, string>): string {
  const d = e.deployment;
  if (d) {
    const ref = d.ref?.replace(/^refs\/(heads|tags)\//, "");
    return ["GitHub Actions", ref, d.commit_sha?.slice(0, 12)].filter(Boolean).join(" · ");
  }
  return e.actor ? `uploaded via ${whoActed(e.actor, subjects)}` : "uploaded by hand";
}

export function changes(events: AccessEvent[], subjects?: Map<string, string>): Change[] {
  const out: Change[] = [];
  for (const e of events) {
    if (!isChange(e.type)) continue;
    const base = { time: e.time, type: e.type };
    const host = e.host ?? "";
    switch (e.type) {
      case "pages.deployed":
        out.push({ ...base, title: `${host || e.deployment?.project || "A site"} was updated`, detail: "", source: deploySource(e, subjects), tone: "good", undo: host ? { site: host } : undefined });
        break;
      case "pages.rolled_back":
        out.push({ ...base, title: `${host || "A site"} went back to a previous version`, detail: e.actor ? `By ${whoActed(e.actor, subjects)}.` : "", tone: "neutral" });
        break;
      case "pages.deploy_refused":
      case "pages.refused":
      case "pages.failed": {
        const name = e.deployment?.project || host || "a site";
        out.push({ ...base, title: `An update to ${name} did not go live`, detail: [e.code, e.reason].filter(Boolean).join(" · ") || "It was refused.", tone: "bad" });
        break;
      }
      case "config.reloaded":
        out.push({ ...base, title: "Configuration reloaded", detail: e.actor ? `Triggered by ${whoActed(e.actor, subjects)}.` : "", tone: "neutral" });
        break;
      case "config.reload_failed":
        out.push({ ...base, title: "A configuration reload failed", detail: "The previous configuration is still in use.", tone: "bad" });
        break;
      case "route.created": case "route.deleted":
        out.push({ ...base, title: `${host || "A route"} was ${e.type === "route.created" ? "added" : "removed"}`, detail: e.actor ? `By ${whoActed(e.actor, subjects)}.` : "", tone: "neutral" });
        break;
      case "user.invited": case "user.disabled": case "user.enabled":
        out.push({ ...base, title: `${host || "A person"} was ${e.type.slice(5)}`, detail: e.actor ? `By ${whoActed(e.actor, subjects)}.` : "", tone: "neutral" });
        break;
      case "passkey.registered": {
        // The event's host is the login page, not the person: only the actor says who.
        const who = whoActed(e.actor, subjects);
        out.push({ ...base, title: `${who && !who.startsWith("sb:") ? who : "Someone"} added a passkey`, detail: e.code === "device_bound" ? "A passkey that stays on one device." : "A passkey that can sync across devices.", tone: "neutral" });
        break;
      }
      case "passkey.revoked":
        out.push({ ...base, title: `A passkey was revoked${host ? ` for ${host}` : ""}`, detail: e.reason ?? "", tone: "neutral" });
        break;
      case "tunnel.connected": case "tunnel.disconnected":
        out.push({ ...base, title: `${host || "A connector"} ${e.type === "tunnel.connected" ? "connected" : "disconnected"}`, detail: "", tone: e.type === "tunnel.connected" ? "good" : "bad" });
        break;
      default:
        out.push({ ...base, title: e.type.replace(/[._]/g, " "), detail: [host, e.reason].filter(Boolean).join(" · "), tone: /failed|rejected|refused/.test(e.type) ? "bad" : "neutral" });
    }
  }
  return out;
}
