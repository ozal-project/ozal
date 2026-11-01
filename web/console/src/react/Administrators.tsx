import { useEffect, useMemo, useState, type FormEvent } from "react";
import { Link } from "react-router";
import { Check, Copy, UserPlus } from "lucide-react";
import { addPasskey, consoleOrigin, explainPasskeyError } from "$lib/passkey";
import { fetchAdmins, inviteAdmin, recoverAdmin, setAdminDisabled, type AdminAccount } from "$lib/api";
import { Button } from "$lib/components/ui/button/react";
import { Input } from "$lib/components/ui/input/react";
import { Modal } from "./Modal";
import { useSession } from "./session";

const toolList = [
  { id: "administrators", label: "Administrators", can: "Invite and disable other administrators (up to your own tools)" },
  { id: "applications", label: "Applications", can: "Add, change and remove applications and routes" },
  { id: "people", label: "People", can: "Invite people, edit groups, disable accounts and passkeys" },
  { id: "sites", label: "Sites", can: "Publish and roll back static sites" },
  { id: "updates", label: "Updates", can: "See when a signed release is available" },
  { id: "updates.install", label: "Install updates", can: "Install a reviewed signed release after confirming with a passkey" },
  { id: "publishing", label: "CI publishing", can: "Choose which repositories may publish from CI" },
  { id: "connectors", label: "Connectors", can: "See private hosts (enrolment stays on the host)" },
];

type LinkResult = { link: string; email: string; expires: string };
export function Administrators() {
  const { session } = useSession();
  const token = session?.token ?? "";
  const mine = useMemo(() => toolList.filter((tool) => session?.tools.includes(tool.id)), [session?.tools]);
  const [admins, setAdmins] = useState<AdminAccount[] | null>(null);
  const [error, setError] = useState("");
  const [addOpen, setAddOpen] = useState(false);
  const [email, setEmail] = useState("");
  const [name, setName] = useState("");
  const [picked, setPicked] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [formError, setFormError] = useState("");
  const [invitation, setInvitation] = useState<LinkResult | null>(null);
  const [recovery, setRecovery] = useState<LinkResult | null>(null);
  const [recoveryOpen, setRecoveryOpen] = useState(false);
  const [notices, setNotices] = useState<Record<string, string>>({});
  const [toggling, setToggling] = useState<AdminAccount | null>(null);
  const [toggleError, setToggleError] = useState("");

  async function load(signal?: AbortSignal) {
    try { setAdmins(await fetchAdmins(signal)); setError(""); }
    catch (cause) { if (!signal?.aborted) setError((cause as Error).message); }
  }
  useEffect(() => { const controller = new AbortController(); void load(controller.signal); return () => controller.abort(); }, [token]);

  function startAdd() { setEmail(""); setName(""); setPicked([]); setFormError(""); setInvitation(null); setAddOpen(true); }
  async function invite(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); if (busy || !picked.length) return;
    setBusy(true); setFormError("");
    try {
      const made = await inviteAdmin(token.trim(), email.trim(), picked, name.trim() || undefined);
      setInvitation({ link: `${consoleOrigin(location.origin)}/overview#enrol=${encodeURIComponent(made.token)}`, email: made.email, expires: made.expires });
      await load();
    } catch (cause) { setFormError((cause as Error).message); }
    finally { setBusy(false); }
  }
  async function reset(account: AdminAccount) {
    setNotices((all) => ({ ...all, [account.id]: "" })); setRecovery(null);
    try {
      const made = await recoverAdmin(token.trim(), account.id);
      setRecovery({ link: `${consoleOrigin(location.origin)}/overview#enrol=${encodeURIComponent(made.token)}`, email: made.email, expires: made.expires });
      setRecoveryOpen(true);
    } catch (cause) { setNotices((all) => ({ ...all, [account.id]: (cause as Error).message })); }
  }
  async function addMine(account: AdminAccount) {
    setNotices((all) => ({ ...all, [account.id]: "" }));
    try { await addPasskey(token.trim()); setNotices((all) => ({ ...all, [account.id]: "Passkey added: you can sign in with either device." })); await load(); }
    catch (cause) { setNotices((all) => ({ ...all, [account.id]: explainPasskeyError(cause) })); }
  }
  async function toggle() {
    if (!toggling) return;
    setBusy(true); setToggleError("");
    try { await setAdminDisabled(token.trim(), toggling.id, !toggling.disabled); setToggling(null); await load(); }
    catch (cause) { setToggleError((cause as Error).message); }
    finally { setBusy(false); }
  }
  const when = (iso?: string) => iso ? new Date(iso).toLocaleString([], { dateStyle: "medium", timeStyle: "short" }) : "never";

  return <div className="grid content-start gap-6">
    <header className="flex flex-wrap items-end justify-between gap-4"><div><p className="plate">Access</p><h1 tabIndex={-1} className="mt-1 text-2xl font-semibold tracking-tight">Administrators</h1><p className="text-muted-foreground mt-1 text-sm">People who can sign in to this console and the tools they can use.</p><nav className="console-tabs" aria-label="People views">{session?.tools.includes("people") && <><Link className="console-tab" to="/people">People</Link><Link className="console-tab" to="/groups">Groups</Link></>}<Link aria-current="page" className="console-tab" to="/administrators">Administrators</Link></nav></div><Button size="sm" onClick={startAdd}><UserPlus className="size-3.5" aria-hidden="true" />Add administrator</Button></header>
    {error && !admins && <p className="border-destructive/40 bg-destructive/10 rounded-xl border px-4 py-3 text-sm" role="alert">Administrators could not be read ({error}). <button className="underline" onClick={() => void load()}>Retry</button></p>}
    {!admins ? <p className="text-muted-foreground text-sm" role="status">Loading administrators…</p> : admins.length === 0 ? <div className="panel px-5 py-10 text-center"><p className="font-medium">No administrator has a passkey account yet</p><p className="text-muted-foreground mx-auto mt-1 max-w-md text-sm">{session?.passkey ? "Invite the people who run this console." : "You are using a host login, which is not an account; invite yourself to get one."}</p><Button className="mt-4" onClick={startAdd}><UserPlus className="size-4" />Add administrator</Button></div> : <><p className="text-muted-foreground -mt-3 text-sm">They run this console with their own passkey and see only the tools they were given.</p><ul className="bg-card divide-y rounded-lg" aria-label="Administrators">{admins.map((account) => <li key={account.id} className="grid gap-x-6 gap-y-2 px-4 py-3 sm:px-5 md:grid-cols-[minmax(0,1.2fr)_minmax(0,1fr)_13rem] md:items-center"><div className="min-w-0"><p className="truncate text-sm font-medium">{account.name}{account.you && <span className="text-muted-foreground ml-2 text-xs font-normal">you</span>}</p><p className="text-muted-foreground flex items-center gap-1.5 truncate text-xs"><span className={`lamp ${account.disabled ? "lamp-off" : "lamp-on"}`} aria-hidden="true" />{account.disabled ? "Disabled" : "Active"} · {account.email} · last sign-in {when(account.last_used_at)}</p></div><ul className="flex min-w-0 flex-wrap gap-1.5" aria-label={`Tools of ${account.email}`}>{account.tools.length === toolList.length ? <li className="chip">All tools</li> : account.tools.map((id) => <li key={id} className="chip">{toolList.find((tool) => tool.id === id)?.label ?? id}</li>)}</ul><div className="flex flex-wrap items-center gap-1 md:justify-end">{account.you ? <Button variant="ghost" size="sm" onClick={() => void addMine(account)}>Add a passkey</Button> : <>{!account.disabled && <Button variant="ghost" size="sm" onClick={() => void reset(account)}>Reset passkeys</Button>}<Button variant="ghost" size="sm" className={account.disabled ? "" : "text-destructive"} onClick={() => { setToggleError(""); setToggling(account); }}>{account.disabled ? "Enable" : "Disable"}</Button></>}</div>{notices[account.id] && <p className="text-muted-foreground text-xs md:col-span-3" role="status">{notices[account.id]}</p>}</li>)}</ul></>}

    <Modal open={addOpen} onOpenChange={setAddOpen} title={invitation ? "Invitation ready" : "Add administrator"} intro={invitation ? "" : "They get a one-time link to create their own passkey. You can give only tools you have."}>{invitation ? <div className="grid gap-3" aria-live="polite"><p className="text-sm font-medium">Invitation for {invitation.email}</p><CopyLink link={invitation.link} /><p className="text-muted-foreground text-xs">Works once, until {when(invitation.expires)}. They open it as <span className="font-mono">ozal.localhost</span> through their own SSH tunnel to this console, give their name and create a passkey. Shown only now.</p><div className="flex justify-end gap-2"><Button variant="ghost" onClick={startAdd}>Add another</Button><Button onClick={() => setAddOpen(false)}>Done</Button></div></div> : <form onSubmit={(event) => void invite(event)} className="grid gap-4"><div className="grid gap-2"><label htmlFor="adm-email" className="text-sm font-medium">Email</label><Input id="adm-email" value={email} onChange={(event) => setEmail(event.target.value)} type="email" required maxLength={254} autoComplete="off" /></div><div className="grid gap-2"><label htmlFor="adm-name" className="text-sm font-medium">Name <span className="text-muted-foreground font-normal">(optional, they can set it)</span></label><Input id="adm-name" value={name} onChange={(event) => setName(event.target.value)} maxLength={80} autoComplete="off" /></div><fieldset><legend className="mb-2 text-sm font-medium">Tools <span className="text-muted-foreground font-normal">· {picked.length} of {mine.length}</span></legend><div className="divide-y rounded-lg border">{mine.map((tool) => <label key={tool.id} className="hover:bg-accent/40 flex cursor-pointer items-start gap-3 px-3 py-2 text-sm"><input type="checkbox" checked={picked.includes(tool.id)} onChange={(event) => setPicked((all) => event.target.checked ? [...all, tool.id] : all.filter((id) => id !== tool.id))} className="mt-1 accent-[var(--primary)]" /><span>{tool.label}<span className="text-muted-foreground block text-xs">{tool.can}</span></span></label>)}</div></fieldset>{formError && <p className="text-destructive text-sm" role="alert">{formError}</p>}<div className="flex justify-end gap-2"><Button type="button" variant="ghost" onClick={() => setAddOpen(false)}>Cancel</Button><Button type="submit" disabled={busy || !email.trim() || !picked.length}>{busy ? "Creating…" : "Create invitation"}</Button></div></form>}</Modal>
    <Modal open={recoveryOpen} onOpenChange={setRecoveryOpen} title="Recovery link">{recovery && <div className="grid gap-3" aria-live="polite"><p className="text-sm font-medium">Recovery link for {recovery.email}</p><CopyLink link={recovery.link} /><p className="text-muted-foreground text-xs">Works once, until {when(recovery.expires)}. Opening it replaces every passkey of that person and ends their open sessions. Send it only to them. Shown only now.</p><div className="flex justify-end"><Button onClick={() => setRecoveryOpen(false)}>Done</Button></div></div>}</Modal>
    <Modal open={!!toggling} onOpenChange={(open) => { if (!open) setToggling(null); }} title={toggling?.disabled ? "Enable this administrator?" : "Disable this administrator?"} intro="Review change"><dl className="border-border divide-border divide-y rounded-lg border text-sm"><ReviewRow label="Target" value={toggling?.email ?? ""} /><ReviewRow label="Current" value={toggling?.disabled ? "Disabled; cannot sign in" : "Active"} /><ReviewRow label="Requested" value={toggling?.disabled ? "Active" : "Disabled"} /><ReviewRow label="Consequence" value={toggling?.disabled ? "They can sign in to the console again with their passkey." : "They cannot sign in to the console, and their open console sessions end."} /><ReviewRow label="Details" value="Their tools and passkeys are kept." /></dl>{toggleError && <p className="text-destructive mt-3 text-sm" role="alert">{toggleError}</p>}<div className="mt-5 flex justify-end gap-2"><Button variant="outline" disabled={busy} onClick={() => setToggling(null)}>Cancel</Button><Button variant={toggling?.disabled ? "default" : "destructive"} disabled={busy} onClick={() => void toggle()}>{busy ? "Saving…" : toggling?.disabled ? "Enable administrator" : "Disable administrator"}</Button></div></Modal>
  </div>;
}

function ReviewRow({ label, value }: { label: string; value: string }) { return <div className="grid gap-1 px-3 py-2 sm:grid-cols-[7rem_1fr]"><dt className="text-muted-foreground">{label}</dt><dd>{value}</dd></div>; }
function CopyLink({ link }: { link: string }) {
  const [copied, setCopied] = useState(false);
  async function copy() { try { await navigator.clipboard.writeText(link); setCopied(true); window.setTimeout(() => setCopied(false), 1800); } catch { setCopied(false); } }
  return <div className="bg-background border-border flex items-start gap-2 rounded-lg border py-2 pr-2 pl-3"><code className="min-w-0 flex-1 break-all font-mono text-xs">{link}</code><button type="button" onClick={() => void copy()} aria-label="Copy invitation link" className="text-muted-foreground hover:text-foreground shrink-0 rounded-md p-1.5 focus-visible:outline-2">{copied ? <Check className="text-success size-4" aria-hidden="true" /> : <Copy className="size-4" aria-hidden="true" />}</button><span className="sr-only" role="status">{copied ? "Copied" : ""}</span></div>;
}
