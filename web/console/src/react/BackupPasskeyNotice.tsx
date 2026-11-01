import { useCallback, useEffect, useState } from "react";
import { addPasskey, consoleOrigin, explainPasskeyError, issueDeviceLink, listOwnPasskeys, type OwnPasskey } from "$lib/passkey";
import { needsBackup, readLater, rememberLater } from "$lib/backupPasskey";
import { Button } from "$lib/components/ui/button/react";

export function BackupPasskeyNotice({ token }: { token: string }) {
  const [passkeys, setPasskeys] = useState<OwnPasskey[] | null>(null);
  const [later, setLater] = useState(() => { try { return readLater(sessionStorage); } catch { return false; } });
  const [adding, setAdding] = useState(false);
  const [notice, setNotice] = useState("");
  const [deviceLink, setDeviceLink] = useState("");
  const [deviceUntil, setDeviceUntil] = useState("");
  const load = useCallback(async (signal?: AbortSignal) => {
    try { setPasskeys(await listOwnPasskeys(token, signal)); }
    catch { if (!signal?.aborted) setPasskeys(null); }
  }, [token]);

  useEffect(() => {
    const controller = new AbortController();
    void load(controller.signal);
    const focus = () => { if (!later) void load(); };
    addEventListener("focus", focus);
    return () => { controller.abort(); removeEventListener("focus", focus); };
  }, [load, later]);

  async function addHere() {
    if (adding) return;
    setAdding(true); setNotice("");
    try { await addPasskey(token); setNotice("Passkey added. You can sign in with either device."); await load(); }
    catch (cause) { setNotice(explainPasskeyError(cause)); }
    finally { setAdding(false); }
  }

  async function addElsewhere() {
    setNotice(""); setDeviceLink("");
    try {
      const made = await issueDeviceLink(token);
      setDeviceLink(`${consoleOrigin(location.origin)}/overview#enrol=${encodeURIComponent(made.token)}`);
      setDeviceUntil(new Date(made.expires).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }));
    } catch (cause) { setNotice((cause as Error).message); }
  }

  if (!needsBackup(passkeys, later)) return null;
  return <section className="bg-card mb-6 grid gap-3 rounded-lg px-4 py-3.5" aria-labelledby="backup-passkey-title">
    <div><h2 id="backup-passkey-title" className="text-sm font-semibold">Add a second passkey</h2><p className="text-muted-foreground mt-1 text-sm">You have one passkey for this console. If you lose it, only someone on the host can let you back in. Keep another one somewhere else: a security key, a password manager or another device.</p></div>
    {notice && <p className="text-muted-foreground text-xs" role="status">{notice}</p>}
    {deviceLink && <div className="grid gap-2" aria-live="polite"><p className="text-xs">Open this on the other device or browser before {deviceUntil}. It works once and creates a passkey there for you.</p><label className="grid gap-1 text-xs" htmlFor="backup-device-link">One-use device link</label><input id="backup-device-link" readOnly value={deviceLink} onFocus={(event) => event.currentTarget.select()} className="bg-background border-border min-w-0 rounded-md border px-2 py-1.5 font-mono text-xs" /><Button variant="outline" size="sm" className="w-fit" onClick={() => void navigator.clipboard.writeText(deviceLink).then(() => setNotice("Device link copied."), () => setNotice("Select the link above and copy it manually."))}>Copy link for another device</Button></div>}
    <div className="flex flex-wrap gap-2"><Button size="sm" disabled={adding} onClick={() => void addHere()}>{adding ? "Waiting for your device…" : "Add one in this browser"}</Button><Button size="sm" variant="outline" onClick={() => void addElsewhere()}>Use another device</Button><Button size="sm" variant="ghost" onClick={() => { setLater(true); try { rememberLater(sessionStorage); } catch { /* Storage can be unavailable. */ } }}>Not now</Button></div>
  </section>;
}
