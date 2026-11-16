import { useEffect, useState } from "react";
import { confirmWithPasskey, listOwnPasskeys, removeOwnPasskey, renameOwnPasskey, type OwnPasskey } from "$lib/passkey";
import { passkeyName, passkeyPlace } from "$lib/passkeyName";
import { withPasskeyProof } from "$lib/accessRequests";
import { Button } from "$lib/components/ui/button/react";
import { Modal } from "./Modal";

type Props = { open: boolean; onOpenChange(open: boolean): void; token: string; portalContainer?: HTMLElement };

const name = passkeyName;
const where = (passkey: OwnPasskey) => passkeyPlace(passkey) ? `${passkeyPlace(passkey)} · ` : "";
const when = (value?: string) => value ? new Date(value).toLocaleString([], { dateStyle: "medium", timeStyle: "short" }) : "never";

export function PasskeysDialog({ open, onOpenChange, token, portalContainer }: Props) {
  const [passkeys, setPasskeys] = useState<OwnPasskey[] | null>(null);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [pending, setPending] = useState<OwnPasskey | null>(null);
  const [busy, setBusy] = useState(false);
  const [editing, setEditing] = useState<OwnPasskey | null>(null);
  const [draft, setDraft] = useState("");

  async function load(signal?: AbortSignal) {
    setError("");
    try { setPasskeys(await listOwnPasskeys(token, signal)); }
    catch (cause) { if (!signal?.aborted) setError((cause as Error).message); }
  }

  useEffect(() => {
    if (!open) return;
    const controller = new AbortController();
    setPasskeys(null); setNotice(""); setPending(null); setEditing(null);
    void load(controller.signal);
    return () => controller.abort();
  }, [open, token]);

  async function remove() {
    if (!pending || busy) return;
    const target = pending;
    setBusy(true); setError(""); setNotice("");
    try {
      await removeOwnPasskey(token, target.id);
      setNotice(`${name(target)} was removed. It can no longer sign in.`);
    } catch (cause) { setError((cause as Error).message); }
    finally { setBusy(false); setPending(null); await load(); }
  }

  async function rename(event: React.FormEvent) {
    event.preventDefault();
    if (!editing || busy) return;
    const label = draft.trim();
    if (!label) { setError("Give it a name, or cancel."); return; }
    setBusy(true); setError(""); setNotice("");
    try {
      await withPasskeyProof(token, () => renameOwnPasskey(token, editing.id, label), confirmWithPasskey);
      setNotice(`Renamed to ${label}.`);
      setEditing(null);
      await load();
    } catch (cause) { setError((cause as Error).message); }
    finally { setBusy(false); }
  }

  return <>
    <Modal open={open} onOpenChange={onOpenChange} title="Your passkeys" intro="Name them so you know where each one lives. Remove one you no longer have, like a lost phone: anyone signed in with it is signed out at once." portalContainer={portalContainer}>
      <div className="grid gap-4">
        {error && <p className="text-destructive text-sm" role="alert">{error}</p>}
        {notice && <p className="text-sm" role="status">{notice}</p>}
        {passkeys === null && !error ? <p className="text-muted-foreground text-sm" role="status">Reading…</p> : passkeys && <>
          <ul className="divide-border border-border divide-y rounded-lg border" aria-label="Passkeys">
            {passkeys.map((passkey) => <li key={passkey.id} className="flex flex-wrap items-center gap-3 px-3 py-3">
              {editing?.id === passkey.id ? <form className="flex min-w-0 flex-1 flex-wrap items-center gap-2" onSubmit={(event) => void rename(event)}>
                <label className="sr-only" htmlFor="passkey-name">Name for this passkey</label>
                <input id="passkey-name" autoFocus maxLength={60} value={draft} onChange={(event) => setDraft(event.target.value)} placeholder="e.g. YubiKey, Work laptop" className="bg-background border-border min-w-0 flex-1 rounded-md border px-2 py-1.5 text-sm" />
                <Button size="sm" type="submit" disabled={busy}>{busy ? "Saving…" : "Save"}</Button>
                <Button size="sm" variant="ghost" type="button" disabled={busy} onClick={() => setEditing(null)}>Cancel</Button>
              </form> : <>
                <div className="min-w-0 flex-1"><p className="text-sm font-medium">{name(passkey)}{passkey.current && <span className="text-signal ml-2 text-xs font-normal">this session</span>}</p><p className="text-muted-foreground text-xs">{where(passkey)}Added {when(passkey.created_at)} · last used {when(passkey.last_used_at)}</p></div>
                <Button variant="ghost" size="sm" disabled={busy} aria-label={`Rename ${name(passkey)}`} onClick={() => { setEditing(passkey); setDraft(passkey.label ?? ""); setError(""); }}>Rename</Button>
                {!passkey.current && <Button variant="outline" size="sm" disabled={busy} aria-label={`Remove ${name(passkey)}, added ${when(passkey.created_at)}`} onClick={() => setPending(passkey)}>Remove</Button>}
              </>}
            </li>)}
          </ul>
          {passkeys.length === 1 && <p className="text-muted-foreground text-xs">This is your only passkey. Add another from your menu before you need it.</p>}
        </>}
        <div className="flex justify-end"><Button variant="outline" onClick={() => onOpenChange(false)}>Close</Button></div>
      </div>
    </Modal>
    <Modal open={!!pending} onOpenChange={(next) => { if (!next && !busy) setPending(null); }} title="Remove this passkey?" intro="Review change" portalContainer={portalContainer}>
      <dl className="border-border divide-border divide-y rounded-lg border text-sm">
        <ReviewRow label="Target" value={pending ? name(pending) : ""} />
        <ReviewRow label="Current" value={pending ? `Can sign in · added ${when(pending.created_at)}` : ""} />
        <ReviewRow label="Requested" value="Removed" />
        <ReviewRow label="Consequence" value="It can no longer sign in to this console, and any session opened with it ends now. Your other passkeys keep working." />
      </dl>
      {error && <p className="text-destructive mt-3 text-sm" role="alert">{error}</p>}
      <div className="mt-5 flex justify-end gap-2"><Button variant="outline" disabled={busy} onClick={() => setPending(null)}>Cancel</Button><Button variant="destructive" disabled={busy} onClick={() => void remove()}>{busy ? "Removing…" : "Remove passkey"}</Button></div>
    </Modal>
  </>;
}

function ReviewRow({ label, value }: { label: string; value: string }) { return <div className="grid gap-1 px-3 py-2 sm:grid-cols-[7rem_1fr]"><dt className="text-muted-foreground">{label}</dt><dd className="break-words">{value}</dd></div>; }
