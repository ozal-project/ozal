// The operator credential, shared by the console pages that change state. It
// lives in this tab's memory, never in storage or the URL. A reload gets it
// back from the edge, which keeps it in an HttpOnly cookie that only its
// resume endpoint receives (see resumeSession).
// `tools` are the areas the host login granted; a pasted admin token is the
// host's own credential and carries every tool.
export const tools = [
  { id: "administrators", label: "Administrators", can: "Invite and disable other administrators (up to your own tools)" },
  { id: "applications", label: "Applications", can: "Add, change and remove applications and routes" },
  { id: "people", label: "People", can: "Invite people, edit groups, disable accounts and passkeys" },
  { id: "sites", label: "Sites", can: "Publish and roll back static sites" },
  { id: "updates", label: "Updates", can: "See when a signed release is available" },
  { id: "publishing", label: "CI publishing", can: "Choose which repositories may publish from CI" },
  { id: "connectors", label: "Connectors", can: "See private hosts (enrolment stays on the host)" },
] as const;
export type Tool = (typeof tools)[number]["id"];

// `enrol` is a host-issued enrolment link waiting for a name and a passkey.
// `notice` says why the tab is signed out (expired, revoked, host refused).
// `ready` turns true once a host login link in the URL has been tried, so the
// console does not flash its sign-in panel for someone who just arrived with one.
// `passkey` is true when the session came from an administrator passkey (it
// has an account to add passkeys to); host logins and tokens do not.
// `renewable` sessions (passkey sign-ins) are extended while the person is
// active; the others end at `expires` whatever happens.
export const admin = $state({ token: "", asked: 0, name: "", expires: "", tools: [] as string[], notice: "", ready: false, enrol: "", passkey: false, renewable: false });

export type SessionStart = { token: string; name?: string; expires?: string; tools?: string[]; renewable?: boolean };

// startSession is the one place a tab becomes signed in.
export function startSession(s: SessionStart, passkey: boolean) {
  admin.token = s.token; admin.name = s.name ?? ""; admin.expires = s.expires ?? ""; admin.tools = s.tools ?? [];
  admin.renewable = !!s.renewable; admin.passkey = passkey; admin.notice = ""; admin.enrol = "";
}

// can reports whether this tab's session may use a tool. Locked means no.
export const can = (tool: Tool) => !!admin.token && admin.tools.includes(tool);

export const signOut = () => { admin.token = ""; admin.name = ""; admin.expires = ""; admin.tools = []; admin.passkey = false; admin.renewable = false; };

// endSession signs out because the edge no longer accepts this credential.
export function endSession(notice = "This session is no longer valid. Sign in again.") {
  if (!admin.token) return;
  signOut();
  admin.notice = notice;
}

// askForToken is how a page that needs a session says so. The console only
// renders with a session, so this is a guard for a session that just ended:
// the sign-in screen replaces the console as soon as the token is cleared.
export function askForToken() {
  admin.asked++;
}
