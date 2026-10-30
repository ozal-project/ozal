// What the Overview page says, as pure functions over data the console already
// reads (status, live events). Wording lives here, not in the page, so it is
// tested and the page only lays it out. Nothing here decides access.
import type { AccessEvent, CertificateStatus, RouteStatus, StatusResponse } from "./api";
import { eventReason, friendlyHealth, whoActed } from "./wording.ts";

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

export type CertificateView = {
  names: string[];
  expires: string;
  renews: string;
  // "bad" when the certificate is not valid or runs out within `soonDays`.
  tone: "good" | "bad";
  note: string;
};

// A renewal normally lands ~30 days before expiry, so under this many days
// left means the edge is overdue even though the certificate still works.
const soonDays = 14;
const dayMs = 86_400_000;

const dateOf = (iso: string): string => {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "unknown";
  return d.toLocaleDateString([], { year: "numeric", month: "short", day: "numeric" });
};

export function certificateView(cert: CertificateStatus | undefined, now: Date): CertificateView | null {
  if (!cert) return null;
  const names = cert.names?.length ? cert.names : cert.host ? cert.host.split(", ") : [];
  const end = new Date(cert.not_after);
  const left = Number.isNaN(end.getTime()) ? null : Math.floor((end.getTime() - now.getTime()) / dayMs);
  const expires = left === null ? "unknown" : left < 0 ? `expired on ${dateOf(cert.not_after)}` : `${dateOf(cert.not_after)} (${plural(left, "day")} left)`;
  const renews = cert.next_attempt ? dateOf(cert.next_attempt) : "not scheduled";
  const bad = !cert.valid || (left !== null && left < soonDays);
  const note = cert.last_error ? `The last renewal attempt failed: ${cert.last_error}.` : !cert.valid ? "It is not valid right now." : bad ? "It expires soon and has not been renewed." : "Renewed automatically.";
  return { names, expires, renews, tone: bad ? "bad" : "good", note };
}

const plural = (n: number, one: string, many = `${one}s`) => `${n} ${n === 1 ? one : many}`;

export function headline(status: StatusResponse | null, needAttention: number): string {
  if (!status) return "Loading…";
  const sites = status.routes.filter((r) => r.is_static).length;
  const apps = status.routes.length - sites;
  const configured = [apps > 0 ? plural(apps, "app route") : "", sites > 0 ? plural(sites, "site route") : ""].filter(Boolean).join(" and ") || "No routes";
  const state = needAttention === 0 ? "No active alerts." : needAttention === 1 ? "One thing needs you." : `${needAttention} things need you.`;
  return `${configured} configured. ${state}`;
}

export function attention(status: StatusResponse | null, now: Date = new Date()): Attention[] {
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
  const view = certificateView(cert, now);
  if (cert && view && (view.tone === "bad" || cert.last_error)) {
    items.push({
      kind: "certificate",
      title: `The certificate for ${cert.host} needs attention`,
      detail: view.note === "Renewed automatically." ? `The last renewal attempt failed: ${cert.last_error}.` : view.note,
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

export type AppRow = { host: string; name: string; state: "down" | "up" | "unchecked"; label: string };

// The application routes as one short list: the ones not answering first, then
// by name. Static sites are left to the Sites list beside it.
export function applications(routes: RouteStatus[]): AppRow[] {
  const rank = { down: 0, up: 1, unchecked: 1 } as const;
  return routes
    .filter((r) => !r.is_static)
    .map((r): AppRow => {
      const state = r.state_class === "unhealthy" ? "down" : r.state_class === "healthy" ? "up" : "unchecked";
      const label = state === "down" ? "not answering" : r.access === "Protected" ? "sign-in" : "public";
      return { host: r.host, name: r.host.replace(/:\d+$/, ""), state, label };
    })
    .sort((a, b) => rank[a.state] - rank[b.state] || a.name.localeCompare(b.name));
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
      case "github.workflow_proposed":
        out.push({ ...base, title: `A publishing workflow was proposed for ${host || "a project"}`, detail: "Review its pull request in GitHub before merging. Publication is not yet verified.", tone: "neutral" });
        break;
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
      case "operator.signed_in":
        out.push({ ...base, title: `${whoActed(e.actor, subjects) || "Someone"} signed in to the console`, detail: e.reason === "passkey" ? "With a passkey." : "With a host login.", tone: "neutral" });
        break;
      case "operator.signed_out":
        out.push({ ...base, title: `${whoActed(e.actor, subjects) || "Someone"} signed out`, detail: "", tone: "neutral" });
        break;
      case "operator.login_issued":
        out.push({ ...base, title: `A host login was issued for ${host || "an operator"}`, detail: eventReason(e.reason), tone: "neutral" });
        break;
      case "operator.revoked":
        out.push({ ...base, title: "A console session was ended from the host", detail: "", tone: "neutral" });
        break;
      case "admin.invited":
        out.push({ ...base, title: `${host || "An administrator"} was invited to the console`, detail: [eventReason(e.reason), e.actor ? `By ${whoActed(e.actor, subjects)}.` : ""].filter(Boolean).join(" · "), tone: "neutral" });
        break;
      default:
        out.push({ ...base, title: e.type.replace(/[._]/g, " "), detail: [host, eventReason(e.reason)].filter(Boolean).join(" · "), tone: /failed|rejected|refused/.test(e.type) ? "bad" : "neutral" });
    }
  }
  return out;
}

export type StreamState = "locked" | "connecting" | "live" | "reconnected" | "lost";
type FeedView = { missed: number; restarted: boolean; partial: boolean };

const timeOf = (d: Date) => d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });

// What the page must say about its own data before anyone reads a count: the
// live view can stop, skip events or span a restart, and rows already on
// screen would otherwise look complete.
export function streamNotices(stream: StreamState, feed: FeedView, held: number): string[] {
  const out: string[] = [];
  if (stream === "lost" && held > 0) out.push("Live updates stopped and are retrying. What is shown below may be out of date.");
  if (feed.missed > 0) out.push(`${plural(feed.missed, "event")} did not reach this page (a slow connection, or history the edge no longer holds): the counts and changes below are incomplete.`);
  if (feed.restarted) out.push("Ozal restarted while this page was open. Rows from before the restart come from the previous run.");
  return out;
}

// Which events the access counts cover. They are a live view held in memory,
// never the whole history; say so whenever older events are already gone.
export function accessScope(stream: StreamState, feed: FeedView, oldest: Date | null, held: number): string {
  if (stream === "locked") return "Sign in to see access decisions.";
  if (held === 0 || !oldest) return stream === "lost" ? "The event stream is unavailable." : stream === "connecting" ? "Connecting to the live stream…" : "Waiting for the first events.";
  if (feed.partial) return `Last ${plural(held, "event")}, since ${timeOf(oldest)}. Older ones are no longer held.`;
  if (feed.restarted) return `${plural(held, "event")} since ${timeOf(oldest)}, across a restart.`;
  return `All ${plural(held, "event")} since this edge started.`;
}
