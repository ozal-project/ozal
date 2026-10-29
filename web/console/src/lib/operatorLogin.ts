// Host-issued one-use links arrive in the URL fragment. Consume them at once;
// they are never put in storage, cookies, a query string or outbound
// navigation.
export type HostLink = { kind: "operator" | "enrol"; token: string };

export function takeHostLink(): HostLink | null {
  const fragment = new URLSearchParams(location.hash.slice(1));
  const operator = fragment.get("operator");
  const enrol = fragment.get("enrol");
  const link: HostLink | null = enrol ? { kind: "enrol", token: enrol } : operator ? { kind: "operator", token: operator } : null;
  if (link) history.replaceState(null, "", location.pathname + location.search);
  return link;
}

export async function exchangeHostLogin(token: string): Promise<{ token: string; name: string; expires: string; tools: string[] }> {
  const res = await fetch("/_/api/operators/exchange", {
    method: "POST", headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ token }), cache: "no-store", signal: AbortSignal.timeout(15_000),
  });
  if (!res.ok) throw new Error("This host login expired or was already used. Open a new link from ozal console login on the host.");
  return res.json();
}
