// Words for people, not for logs. The console shows what an operator needs to
// decide ("nothing is listening at 127.0.0.1:19199"), not the Go error or the
// throwaway port an admin request came from. Pure, so it is tested; anything
// unrecognised is shown as it is, never hidden.

const address = /^(\[[0-9a-f:]+\]|\d{1,3}(?:\.\d{1,3}){3}):\d+$/i;

// subjectsOf maps the opaque id a passkey session leaves in events ("sb:…") to
// the person's email, so the console can say who without changing what the
// audit log records.
export function subjectsOf(people: { email: string; subject?: string }[]): Map<string, string> {
  return new Map(people.filter((p) => p.subject).map((p) => [p.subject as string, p.email]));
}

export function whoActed(actor?: string, subjects?: Map<string, string>): string {
  if (!actor) return "";
  const known = subjects?.get(actor);
  if (known) return known;
  if (address.test(actor)) return "the admin API";
  if (actor === "admin") return "the admin token";
  if (actor === "local") return "the host";
  if (actor.startsWith("operator:")) return `${actor.slice(9)} (host login)`;
  if (actor.startsWith("admin:")) return actor.slice(6);
  if (actor.startsWith("signal:")) return `a reload signal (${actor.slice(7)})`;
  return actor;
}

export function friendlyHealth(raw: string): string {
  const text = raw.trim();
  if (!text) return "it did not answer its health check";
  const refused = /dial tcp ([^\s:]+(?::\d+)?)[^\n]*connection refused/.exec(text) ?? /dial tcp ([\S]+): connect: connection refused/.exec(text);
  if (refused) return `nothing is listening at ${refused[1].replace(/:$/, "")}`;
  if (/no such host/.test(text)) return "its hostname does not exist";
  if (/deadline exceeded|timed out|timeout/i.test(text)) return "it took too long to answer";
  if (/x509|certificate/i.test(text)) return "its certificate is not trusted";
  const status = /(?:status|HTTP)\s+(\d{3})/i.exec(text);
  if (status) return `it answered with an error (${status[1]})`;
  return text.length > 90 ? text.slice(0, 90) : text;
}

// shortId is the first characters of a content hash, enough to tell builds apart.
export const shortId = (id?: string): string => (id ? id.slice(0, 12) : "");

// eventReason keeps the audit text but reads as a sentence: a tool list loses
// its machine commas ("tools: people,sites" becomes "tools: people, sites").
export function eventReason(reason?: string): string {
  if (!reason) return "";
  return reason.replace(/^tools: (.*)$/, (_, list: string) => `tools: ${list.split(",").map((t) => t.trim()).filter(Boolean).join(", ")}`);
}
