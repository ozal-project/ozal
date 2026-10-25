// A tiny History API router: a few fixed pages do not need a routing library.
// The Go handler serves index.html for each of these paths.
export const pages = ["/activity", "/routes", "/people", "/sites", "/simulator"] as const;
export type Page = (typeof pages)[number];

const normalize = (p: string): Page => ((pages as readonly string[]).includes(p) ? (p as Page) : "/activity");

export const route = $state({ path: normalize(location.pathname), search: location.search });

export function navigate(to: Page, context?: { host?: string }) {
  const safe = new URLSearchParams();
  const source = context ?? (to === "/routes" || to === "/simulator" ? Object.fromEntries(new URLSearchParams(location.search)) : {});
  const host = source.host;
  if (typeof host === "string" && host.trim()) safe.set("host", host.trim());
  const search = safe.size ? `?${safe.toString()}` : "";
  const url = `${to}${search}`;
  if (url === `${route.path}${route.search}`) return;
  history.pushState(null, "", url);
  route.path = to;
  route.search = search;
}

addEventListener("popstate", () => {
  route.path = normalize(location.pathname);
  route.search = location.search;
});
