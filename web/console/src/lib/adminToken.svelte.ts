// The operator credential, shared by the console pages that change state. It
// lives only in this tab's memory: never storage, cookies or the URL.
// `tools` are the areas the host login granted; a pasted admin token is the
// host's own credential and carries every tool.
export const tools = [
  { id: "administrators", label: "Administrators", can: "Invite and disable other administrators (up to your own tools)" },
  { id: "applications", label: "Applications", can: "Add, change and remove applications and routes" },
  { id: "people", label: "People", can: "Invite people, edit groups, disable accounts and passkeys" },
  { id: "sites", label: "Sites", can: "Publish and roll back static sites" },
  { id: "publishing", label: "CI publishing", can: "Choose which repositories may publish from CI" },
  { id: "connectors", label: "Connectors", can: "See private hosts (enrolment stays on the host)" },
] as const;
export type Tool = (typeof tools)[number]["id"];

// `enrol` is a host-issued enrolment link waiting for a name and a passkey.
// `notice` says why the tab is signed out (expired, revoked, host refused).
// `ready` turns true once a host login link in the URL has been tried, so the
// console does not flash its sign-in panel for someone who just arrived with one.
export const admin = $state({ token: "", asked: 0, name: "", expires: "", tools: [] as string[], notice: "", ready: false, enrol: "" });

// can reports whether this tab's session may use a tool. Locked means no.
export const can = (tool: Tool) => !!admin.token && admin.tools.includes(tool);

export const signOut = () => { admin.token = ""; admin.name = ""; admin.expires = ""; admin.tools = []; };

// endSession signs out because the edge no longer accepts this credential.
export function endSession(notice = "This session is no longer valid. Sign in again.") {
  if (!admin.token) return;
  signOut();
  admin.notice = notice;
}

// askForToken is how a page that needs a session says so: the Admin menu in
// the header opens, wherever the person was looking on the page.
export function askForToken() {
  admin.asked++;
}
