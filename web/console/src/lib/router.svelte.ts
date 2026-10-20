// A tiny History API router: four fixed pages do not need a routing library.
// The Go handler serves index.html for each of these paths.
export const pages = ["/activity", "/routes", "/simulator"] as const;
export type Page = (typeof pages)[number];

const normalize = (p: string): Page => ((pages as readonly string[]).includes(p) ? (p as Page) : "/activity");

export const route = $state({ path: normalize(location.pathname) });

export function navigate(to: Page) {
  if (to === route.path) return;
  history.pushState(null, "", to);
  route.path = to;
}

addEventListener("popstate", () => {
  route.path = normalize(location.pathname);
});
