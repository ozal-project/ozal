// GitHub sends the browser back to /applications after creating the App
// (code, state) or installing it (setup_action, installation_id). The console
// remembers, per tab, which page the operator left from so it can return
// there. The marker holds only a page path, never anything from GitHub.
export type GitHubReturn = "/applications" | "/integrations";
const key = "ozal.github.return";

export const returningFromGitHub = (search: string) => {
  const q = new URLSearchParams(search);
  return (q.has("code") && q.has("state")) || q.has("setup_action") || q.has("installation_id");
};

export function leaveForGitHub(from: GitHubReturn) {
  try { sessionStorage.setItem(key, from); } catch { /* storage blocked: GitHub returns to Applications */ }
}

export function returnTarget(): GitHubReturn {
  try { return sessionStorage.getItem(key) === "/integrations" ? "/integrations" : "/applications"; }
  catch { return "/applications"; }
}

export function clearReturn() {
  try { sessionStorage.removeItem(key); } catch { /* nothing kept */ }
}
