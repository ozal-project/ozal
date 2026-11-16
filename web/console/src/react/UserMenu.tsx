import { useEffect, useId, useRef, useState } from "react";
import { addPasskey, adminState, consoleOrigin, explainPasskeyError, issueDeviceLink, listOwnPasskeys, ownAccount, type OwnAccount, type OwnPasskey } from "$lib/passkey";
import { initials, passkeyLine, toolSummary } from "$lib/accountMenu";
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
  const [account, setAccount] = useState<OwnAccount | null>(null);
  const [keys, setKeys] = useState<OwnPasskey[] | null>(null);
  const [loginHost, setLoginHost] = useState("");
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

  // The account and its passkeys are read when the menu opens, so the list
  // is current after a passkey is added or removed elsewhere.
  useEffect(() => {
    if (!open || !session?.passkey || !session.token) return;
    const controller = new AbortController();
    const token = session.token.trim();
    ownAccount(token, controller.signal).then(setAccount, () => setAccount(null));
    listOwnPasskeys(token, controller.signal).then(setKeys, () => setKeys(null));
    adminState(controller.signal).then((state) => setLoginHost(state?.login_host ?? ""), () => setLoginHost(""));
    return () => controller.abort();
  }, [open, session?.token, session?.passkey]);

  async function addHere() {
    if (!session?.token || adding) return;
    setAdding(true); setNotice("");
    try {
      await addPasskey(session.token.trim());
      setNotice("Passkey added. You can sign in with either device.");
      setKeys(await listOwnPasskeys(session.token.trim()).catch(() => keys));
    } catch (cause) { setNotice(explainPasskeyError(cause)); }
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
  const sessionLength = session?.renewable ? "signs out after 30 minutes without activity" : session?.expires ? `ends at ${new Date(session.expires).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}` : "lasts until you sign out";
  const shown = (keys ?? []).slice(0, 3).map((key) => ({ id: key.id, ...passkeyLine(key) }));
  const visitorPortal = loginHost ? `${loginHost.replace(/\/$/, "")}/_/auth/me` : "";

  return <>
    <div ref={root} className={placement === "down" ? "console-account relative" : "relative w-full max-w-none"}>
      <button ref={trigger} type="button" aria-haspopup="dialog" aria-label={name} aria-expanded={open} aria-controls={menuID} onClick={() => { setOpen((value) => !value); setNotice(""); }} className={`flex cursor-pointer list-none items-center gap-2 rounded-md py-2 text-sm hover:bg-muted ${placement === "up" ? "w-full px-2 text-left" : "px-2"}`}>
        <span className="grid size-7 place-items-center rounded-full bg-muted text-xs" aria-hidden="true">{initials(name)}</span><span className={`min-w-0 truncate ${placement === "down" ? "console-account-name max-w-32" : "flex-1"}`}>{session?.name || "Operator"}{placement === "up" && <span className="text-muted-foreground block text-xs">{tools.length === 8 ? "Full access" : "Limited access"}</span>}</span>
      </button>
      {open && <div id={menuID} role="dialog" aria-label="Your session" className={`bg-popover border-border absolute z-40 grid w-[min(24rem,calc(100vw-2rem))] overflow-hidden rounded-lg border shadow-xl ${placement === "up" ? "bottom-full left-0 mb-2" : "top-full right-0 mt-2"}`}>
        <div className="flex items-start gap-3 p-4">
          <span className="bg-muted grid size-10 shrink-0 place-items-center rounded-full text-sm font-semibold" aria-hidden="true">{initials(name)}</span>
          <div className="grid min-w-0 gap-0.5">
            <div className="flex flex-wrap items-center gap-2"><h2 className="text-sm font-semibold">{name}</h2><span className="border-border text-muted-foreground rounded-full border px-2 py-px text-[11px] font-medium">{session?.passkey ? "Administrator" : "Host login"}</span></div>
            {account?.email && <p className="text-muted-foreground truncate text-xs">{account.email}</p>}
            <p className="text-muted-foreground text-xs">{session?.passkey ? "Passkey" : "Host authority"} · {sessionLength}</p>
          </div>
        </div>

        <details className="border-border border-t px-4 py-3">
          <summary className="cursor-pointer text-xs font-medium">What this session can do <span className="text-muted-foreground font-normal">· {toolSummary(tools)}</span></summary>
          {tools.length ? <ul className="text-muted-foreground mt-2 grid gap-1 text-xs">{tools.map((tool) => <li key={tool}>{descriptions[tool] ?? tool.replaceAll(".", " ")}</li>)}</ul> : <p className="text-muted-foreground mt-2 text-xs">No tools were granted.</p>}
        </details>

        {session?.passkey && <div className="border-border grid gap-1 border-t p-2">
          <p className="text-muted-foreground px-2 pt-1 text-[11px] font-medium tracking-wide uppercase">Your console passkeys</p>
          {keys === null && <p className="text-muted-foreground px-2 py-1 text-xs">Reading your passkeys…</p>}
          {shown.map((key) => <div key={key.id} className="flex items-center gap-3 rounded-md px-2 py-1.5">
            <span className="min-w-0 flex-1"><span className="block truncate text-sm">{key.name}</span><span className="text-muted-foreground block text-xs">{key.detail}</span></span>
            {key.current && <span className="text-success text-xs font-medium">This sign-in</span>}
          </div>)}
          <button type="button" className="hover:bg-accent rounded-md px-2 py-1.5 text-left text-sm" disabled={adding} onClick={() => void addHere()}>{adding ? "Waiting for your device…" : keys?.length === 1 ? "Add a backup passkey on this device" : "Add a passkey on this device"}</button>
          <button type="button" className="hover:bg-accent rounded-md px-2 py-1.5 text-left text-sm" onClick={() => { trigger.current?.focus(); setOpen(false); setPasskeysOpen(true); }}>Your passkeys</button>
        </div>}

        {account?.also_visitor && <div className="border-border bg-muted/40 border-t px-4 py-3 text-xs">
          <p className="text-muted-foreground">This email also signs in as a <span className="text-foreground">visitor</span>, with its own passkeys.{visitorPortal && <> <a className="link" href={visitorPortal} target="_blank" rel="noreferrer">Visitor account</a></>}</p>
        </div>}

        {notice && <p className="text-muted-foreground border-border border-t px-4 py-2 text-xs" role="status">{notice}</p>}
        {deviceLink && <div className="border-border grid gap-2 border-t px-4 py-3" aria-live="polite"><p className="text-xs">Open this in the other browser before {deviceUntil}. It works once and creates a passkey there for you.</p><label className="grid gap-1 text-xs" htmlFor="device-link">One-use device link</label><input id="device-link" readOnly value={deviceLink} onFocus={(event) => event.currentTarget.select()} className="bg-background border-border min-w-0 rounded-md border px-2 py-1.5 font-mono text-xs" /><Button variant="outline" size="sm" onClick={() => void navigator.clipboard.writeText(deviceLink).then(() => setNotice("Device link copied."), () => setNotice("Select the link above and copy it manually."))}>Copy link for another browser</Button></div>}
        {!session?.passkey && <p className="text-muted-foreground border-border border-t px-4 py-3 text-xs">A host login has no administrator account, so it cannot add passkeys.</p>}

        <div className="border-border grid gap-1 border-t p-2">
          {session?.passkey && <button type="button" className="hover:bg-accent rounded-md px-2 py-1.5 text-left text-sm" onClick={() => void addElsewhere()}>Sign in on another browser</button>}
          <Button variant="outline" size="sm" onClick={logout}>Sign out</Button>
        </div>
      </div>}
    </div>
    <PasskeysDialog open={passkeysOpen} onOpenChange={setPasskeysOpen} token={session?.token ?? ""} portalContainer={root.current ?? undefined} />
  </>;
}
