import { useEffect, useId, useRef, useState } from "react";
import { addPasskey, consoleOrigin, explainPasskeyError, issueDeviceLink } from "$lib/passkey";
import { Button } from "$lib/components/ui/button/react";
import { useSession } from "./session";
import { PasskeysDialog } from "./PasskeysDialog";

const descriptions: Record<string, string> = {
  administrators: "Invite and disable other administrators",
  applications: "Add, change and remove applications and routes",
  people: "Invite people, edit groups, disable accounts and passkeys",
  sites: "Publish and roll back static sites",
  updates: "See when a signed release is available",
  "updates.install": "Install a reviewed release after confirming with a passkey",
  publishing: "Choose which repositories may publish from CI",
  connectors: "See private hosts; enrolment stays on the host",
};

export function UserMenu({ placement = "down" }: { placement?: "up" | "down" }) {
  const { session, logout } = useSession();
  const [open, setOpen] = useState(false);
  const [passkeysOpen, setPasskeysOpen] = useState(false);
  const [adding, setAdding] = useState(false);
  const [notice, setNotice] = useState("");
  const [deviceLink, setDeviceLink] = useState("");
  const [deviceUntil, setDeviceUntil] = useState("");
  const root = useRef<HTMLDivElement>(null);
  const trigger = useRef<HTMLButtonElement>(null);
  const menuID = useId();

  useEffect(() => {
    if (!open) return;
    const outside = (event: PointerEvent) => { if (!root.current?.contains(event.target as Node)) setOpen(false); };
    const keys = (event: KeyboardEvent) => { if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); setOpen(false); trigger.current?.focus(); } };
    addEventListener("pointerdown", outside); addEventListener("keydown", keys);
    return () => { removeEventListener("pointerdown", outside); removeEventListener("keydown", keys); };
  }, [open]);

  async function addHere() {
    if (!session?.token || adding) return;
    setAdding(true); setNotice("");
    try { await addPasskey(session.token.trim()); setNotice("Passkey added. You can sign in with either device."); }
    catch (cause) { setNotice(explainPasskeyError(cause)); }
    finally { setAdding(false); }
  }

  async function addElsewhere() {
    if (!session?.token) return;
    setNotice(""); setDeviceLink("");
    try {
      const made = await issueDeviceLink(session.token.trim());
      setDeviceLink(`${consoleOrigin(location.origin)}/overview#enrol=${encodeURIComponent(made.token)}`);
      setDeviceUntil(new Date(made.expires).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }));
    } catch (cause) { setNotice((cause as Error).message); }
  }

  const name = session?.name || "Admin token";
  const tools = session?.tools ?? [];
  const sessionLength = session?.renewable ? "Signs out after 30 minutes without activity." : session?.expires ? `Ends at ${new Date(session.expires).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}.` : "Lasts until you sign out.";

  return <>
    <div ref={root} className={placement === "down" ? "console-account relative" : "relative w-full max-w-none"}>
      <button ref={trigger} type="button" aria-haspopup="dialog" aria-label={name} aria-expanded={open} aria-controls={menuID} onClick={() => { setOpen((value) => !value); setNotice(""); }} className={`flex cursor-pointer list-none items-center gap-2 rounded-md py-2 text-sm hover:bg-muted ${placement === "up" ? "w-full px-2 text-left" : "px-2"}`}>
        <span className="grid size-7 place-items-center rounded-full bg-muted text-xs" aria-hidden="true">{name.trim().charAt(0).toUpperCase()}</span><span className={`min-w-0 truncate ${placement === "down" ? "console-account-name max-w-32" : "flex-1"}`}>{session?.name || "Operator"}{placement === "up" && <span className="text-muted-foreground block text-xs">{tools.length === 8 ? "Full access" : "Limited access"}</span>}</span>
      </button>
      {open && <div id={menuID} role="dialog" aria-label="Your session" className={`bg-popover border-border absolute z-40 grid w-[min(22rem,calc(100vw-2rem))] gap-3 rounded-lg border p-4 shadow-xl ${placement === "up" ? "bottom-full left-0 mb-2" : "top-full right-0 mt-2"}`}>
        <div><h2 className="text-sm font-medium">{name}</h2><p className="text-muted-foreground mt-1 text-xs">Signed in with {session?.passkey ? "a passkey" : "a host login"}. {sessionLength}</p><p className="text-muted-foreground mt-2 text-xs">This session can use:</p>
          {tools.length ? <ul className="text-muted-foreground mt-1 grid gap-1 text-xs">{tools.map((tool) => <li key={tool}>{descriptions[tool] ?? tool.replaceAll(".", " ")}</li>)}</ul> : <p className="text-muted-foreground mt-1 text-xs">No tools were granted.</p>}
        </div>
        {notice && <p className="text-muted-foreground text-xs" role="status">{notice}</p>}
        {deviceLink && <div className="grid gap-2" aria-live="polite"><p className="text-xs">Open this in the other browser before {deviceUntil}. It works once and creates a passkey there for you.</p><label className="grid gap-1 text-xs" htmlFor="device-link">One-use device link</label><input id="device-link" readOnly value={deviceLink} onFocus={(event) => event.currentTarget.select()} className="bg-background border-border min-w-0 rounded-md border px-2 py-1.5 font-mono text-xs" /><Button variant="outline" size="sm" onClick={() => void navigator.clipboard.writeText(deviceLink).then(() => setNotice("Device link copied."), () => setNotice("Select the link above and copy it manually."))}>Copy link for another browser</Button></div>}
        {session?.passkey && <div className="border-border grid gap-1 border-t pt-3 text-sm">
          <button type="button" className="hover:bg-accent rounded-md px-2 py-1.5 text-left" onClick={() => void addElsewhere()}>Sign in on another browser</button>
          <button type="button" className="hover:bg-accent rounded-md px-2 py-1.5 text-left" disabled={adding} onClick={() => void addHere()}>{adding ? "Waiting for your device…" : "Add a passkey on this device"}</button>
          <button type="button" className="hover:bg-accent rounded-md px-2 py-1.5 text-left" onClick={() => { trigger.current?.focus(); setOpen(false); setPasskeysOpen(true); }}>Your passkeys</button>
        </div>}
        {!session?.passkey && <p className="text-muted-foreground border-border border-t pt-3 text-xs">A host login has no administrator account, so it cannot add passkeys.</p>}
        <Button variant="outline" size="sm" onClick={logout}>Sign out</Button>
      </div>}
    </div>
    <PasskeysDialog open={passkeysOpen} onOpenChange={setPasskeysOpen} token={session?.token ?? ""} portalContainer={root.current ?? undefined} />
  </>;
}
