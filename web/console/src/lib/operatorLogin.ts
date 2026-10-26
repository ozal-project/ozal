// Consume a one-use host-issued fragment immediately. It is never put in
// storage, cookies, a query string, or outbound navigation.
export async function exchangeHostLogin(): Promise<{ token: string; name: string; expires: string; tools: string[] } | null> {
  const fragment = new URLSearchParams(location.hash.slice(1));
  const token = fragment.get("operator");
  if (!token) return null;
  history.replaceState(null, "", location.pathname + location.search);
  const res = await fetch("/_/api/operators/exchange", {
    method: "POST", headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ token }), cache: "no-store", signal: AbortSignal.timeout(15_000),
  });
  if (!res.ok) throw new Error("This host login expired or was already used. Open a new link from switchboard console login on the host.");
  return res.json();
}
