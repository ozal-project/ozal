// A tiny History API router: a few fixed pages do not need a routing library.
// The Go handler serves index.html for each of these paths.
export const pages = ["/connectors", "/applications", "/domains", "/overview", "/activity", "/routes", "/people", "/groups", "/administrators", "/sites"] as const;
export type Page = (typeof pages)[number];

// Old bookmarks: CI projects live inside Sites, and the policy simulator moved
// to the project page (it runs the same engine in the browser).
const moved: Record<string, Page> = { "/projects": "/sites", "/simulator": "/activity" };
const normalize = (p: string): Page => moved[p] ?? ((pages as readonly string[]).includes(p) ? (p as Page) : "/overview");

export const route = $state({ path: normalize(location.pathname), search: location.search });

// The address bar shows the page that is shown: "/" or an old bookmark is
// rewritten in place instead of leaving a URL that names something else.
function syncAddress() {
  if (location.pathname !== route.path) history.replaceState(null, "", route.path + location.search + location.hash);
}
syncAddress();

export function navigate(to: Page, context?: { host?: string }) {
  const safe = new URLSearchParams();
  const source = context ?? (to === "/routes" ? Object.fromEntries(new URLSearchParams(location.search)) : {});
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
  syncAddress();
});
