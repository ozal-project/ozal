import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent } from "react";
import { Link } from "react-router";
import { Copy, RefreshCw, Share2, UserPlus } from "lucide-react";
import { fetchGroups, fetchPeople, invitePerson, revokePasskey, setDisabled, setGroups as savePersonGroups, type Invitation, type People, type Person } from "$lib/api";
import { ageLabel, elapsed, isStale } from "$lib/freshness";
import { parseGroupNames } from "$lib/groupChanges";
import { Button } from "$lib/components/ui/button/react";
import { Input } from "$lib/components/ui/input/react";
import { Modal } from "./Modal";
import { useSession } from "./session";

type Notice = { text: string; ok: boolean };
type Review = { title: string; target: string; current: string; requested: string; consequence: string; detail: string; label: string; commit: () => void };
type MembershipDraft = { email: string; expected: string[]; value: string };

export function People() {
  const { session } = useSession();
  const token = session?.token ?? "";
  const [people, setPeople] = useState<People | null>(null);
  const [known, setKnown] = useState<string[]>([]);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [observedAt, setObservedAt] = useState<number | null>(null);
  const read = useRef<AbortController | null>(null);
  const requestID = useRef(0);
  const [inviteOpen, setInviteOpen] = useState(false);
  const [email, setEmail] = useState("");
  const [name, setName] = useState("");
  const [groups, setGroups] = useState("");
  const [destination, setDestination] = useState("");
  const [inviting, setInviting] = useState(false);
  const inviteController = useRef<AbortController | null>(null);
  const [inviteError, setInviteError] = useState("");
  const [created, setCreated] = useState<Invitation | null>(null);
  const [copied, setCopied] = useState(false);
  const [copyError, setCopyError] = useState("");
  const invitationInput = useRef<HTMLInputElement>(null);
  const [notices, setNotices] = useState<Record<string, Notice>>({});
  const mutations = useRef<Record<string, AbortController>>({});
  const [pendingKeys, setPendingKeys] = useState<string[]>([]);
  const [review, setReview] = useState<Review | null>(null);
  const [keysFor, setKeysFor] = useState<string | null>(null);
  const [keysOpen, setKeysOpen] = useState(false);
  const [membershipDraft, setMembershipDraft] = useState<MembershipDraft | null>(null);
  const [membershipReviewOpen, setMembershipReviewOpen] = useState(false);
  const [membershipError, setMembershipError] = useState("");
  const [membershipSaving, setMembershipSaving] = useState(false);

  const load = useCallback(async () => {
    if (!token.trim()) return;
    read.current?.abort();
    const controller = new AbortController();
    read.current = controller;
    const id = ++requestID.current;
    setLoading(true);
    try {
      const [next, catalog] = await Promise.all([fetchPeople(controller.signal), fetchGroups(token.trim(), controller.signal).catch(() => null)]);
      if (controller.signal.aborted || requestID.current !== id) return;
      setPeople(next);
      if (catalog) setKnown(catalog.map((group) => group.name));
      setError(""); setObservedAt(Date.now());
    } catch (cause) {
      if (!controller.signal.aborted && requestID.current === id) setError((cause as Error).message);
    } finally {
      if (requestID.current === id) { setLoading(false); read.current = null; }
    }
  }, [token]);
  useEffect(() => {
    setPeople(null); setError(""); setObservedAt(null); setCreated(null); setKnown([]);
    void load();
    return () => { requestID.current++; read.current?.abort(); read.current = null; inviteController.current?.abort(); Object.values(mutations.current).forEach((controller) => controller.abort()); mutations.current = {}; };
  }, [load]);
  useEffect(() => { if (!token) return; const timer = window.setInterval(() => void load(), 15_000); return () => window.clearInterval(timer); }, [token, load]);

  const users = people?.users ?? [];
  const active = (person: Person) => person.passkeys.filter((key) => !key.revoked);
  const activeKeys = useMemo(() => users.reduce((count, person) => count + active(person).length, 0), [users]);
  const disabledCount = users.filter((person) => !!person.disabled).length;
  const keysPerson = users.find((person) => person.email === keysFor) ?? null;
  const age = observedAt === null ? "not loaded" : ageLabel(elapsed(Date.now(), observedAt));
  const stale = observedAt !== null && isStale(elapsed(Date.now(), observedAt), Boolean(error));
  const freshness = observedAt === null ? "Not loaded" : error ? "Last read failed" : loading ? "Refreshing" : stale ? "Stale" : "Current";
  const freshnessLamp = observedAt === null ? "lamp-idle" : error || stale ? "lamp-off" : loading ? "lamp-wait" : "lamp-on";
  const membershipChosen = membershipDraft ? parseGroupNames(membershipDraft.value) : [];
  const suggestions = known.filter((group) => !membershipChosen.includes(group));
  const currentInviteGroups = parseGroupNames(groups);
  const pendingForPerson = (personEmail: string) => pendingKeys.find((key) => key.startsWith(`person:${personEmail}:`) || key.startsWith(`passkey:${personEmail}:`) || key === `groups:${personEmail}`);
  const busyForPerson = (personEmail: string) => !!pendingForPerson(personEmail);
  const day = (iso?: string) => iso ? new Date(iso).toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" }) : "—";

  function startInvite() {
    setEmail(""); setName(""); setGroups(""); setDestination("");
    setInviteError(""); setCreated(null); setCopied(false); setCopyError(""); setInviteOpen(true);
  }
  async function invite(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); setInviteError(""); setCreated(null); setCopied(false);
    if (!token.trim()) { setInviteError("Sign in as admin first."); return; }
    setInviting(true);
    const controller = new AbortController(); inviteController.current = controller;
    try {
      const made = await invitePerson(token.trim(), { email: email.trim(), name: name.trim(), groups: currentInviteGroups, return_to: destination ? `https://${destination}/` : "" }, controller.signal);
      setCreated(made); await load();
    } catch (cause) {
      const uncertain = controller.signal.aborted || (cause as Error).name === "TimeoutError";
      setInviteError(uncertain ? "Could not confirm the invitation response. The request may have completed; refresh People before retrying." : (cause as Error).message);
      if (uncertain) await load();
    } finally { if (inviteController.current === controller) { inviteController.current = null; setInviting(false); } }
  }
  function stopInvite() { inviteController.current?.abort(new DOMException("Stopped by operator", "AbortError")); }
  async function copyInvitation() {
    if (!created) return;
    try { await navigator.clipboard.writeText(created.link); setCopied(true); setCopyError(""); }
    catch { setCopied(false); setCopyError("Clipboard access was blocked. The selected invitation link is ready to copy manually."); invitationInput.current?.focus(); invitationInput.current?.select(); }
  }
  async function shareInvitation() { if (created && navigator.share) await navigator.share({ title: "Your Ozal invitation", url: created.link }).catch(() => {}); }

  const act = useCallback(async (key: string, personEmail: string, run: (signal: AbortSignal) => Promise<unknown>, done: string) => {
    if (!token.trim()) { setNotices((all) => ({ ...all, [personEmail]: { text: "Sign in as admin first.", ok: false } })); return; }
    const controller = new AbortController(); mutations.current[key] = controller;
    setPendingKeys((all) => [...all.filter((entry) => entry !== key), key]);
    setNotices((all) => { const next = { ...all }; delete next[personEmail]; return next; });
    try {
      await run(controller.signal);
      setNotices((all) => ({ ...all, [personEmail]: { text: done, ok: true } }));
      await load();
    } catch (cause) {
      const uncertain = controller.signal.aborted || (cause as Error).name === "TimeoutError";
      setNotices((all) => ({ ...all, [personEmail]: { text: uncertain ? "The response was interrupted. This change may have completed; refreshing to reconcile the current state." : (cause as Error).message, ok: false } }));
      if (uncertain) await load();
    } finally {
      if (mutations.current[key] === controller) delete mutations.current[key];
      setPendingKeys((all) => all.filter((entry) => entry !== key));
    }
  }, [load, token]);
  function stopMutation(key: string) { mutations.current[key]?.abort(new DOMException("Stopped by operator", "AbortError")); }

  function reviewToggle(person: Person) {
    const nextDisabled = !person.disabled;
    const key = `person:${person.email}:${nextDisabled ? "disable" : "enable"}`;
    setReview({
      title: nextDisabled ? "Disable this person?" : "Enable this person?", target: person.email,
      current: person.disabled ? "Disabled; sign-in is blocked" : "Enabled; sign-in follows route policy",
      requested: nextDisabled ? "Disabled" : "Enabled",
      consequence: nextDisabled ? "They will not be able to sign in, and their active sessions will end." : "They will be able to sign in again; existing sessions are not restored.",
      detail: "Their other settings stay as they are.", label: nextDisabled ? "Disable person" : "Enable person",
      commit: () => void act(key, person.email, (signal) => setDisabled(token.trim(), person.email, nextDisabled, signal), nextDisabled ? `${person.email} is disabled; their sessions ended.` : `${person.email} can sign in again.`),
    });
  }
  function reviewRevoke(personEmail: string, id: string, provider: string) {
    const key = `passkey:${personEmail}:${id}`;
    setReview({
      title: "Revoke this passkey?", target: `${personEmail} · ${provider} · ${id}`, current: "Active passkey", requested: "Revoked",
      consequence: "The credential stops working and all of this person’s sessions will end.",
      detail: "At least one active passkey is required; the server will refuse to remove the last one.", label: "Revoke passkey",
      commit: () => void act(key, personEmail, (signal) => revokePasskey(token.trim(), personEmail, id, signal), `${provider} passkey revoked; ${personEmail}'s sessions ended.`),
    });
  }
  function reviewMembership(person: Person) {
    setMembershipDraft({ email: person.email, expected: [...person.groups], value: person.groups.join(", ") });
    setMembershipError(""); setMembershipReviewOpen(false); setMembershipDraftOpen(true);
  }
  const [membershipDraftOpen, setMembershipDraftOpen] = useState(false);
  function prepareMembership(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!membershipDraft || !token.trim()) return;
    setMembershipDraft((draft) => draft ? { ...draft, value: parseGroupNames(draft.value).join(", ") } : draft);
    setMembershipDraftOpen(false); setMembershipReviewOpen(true);
  }
  async function saveMembership() {
    if (!membershipDraft) return;
    const draft = membershipDraft;
    const next = parseGroupNames(draft.value);
    setMembershipSaving(true); setMembershipError("");
    try {
      await savePersonGroups(token.trim(), draft.email, draft.expected, next);
      setMembershipDraft(null); setMembershipReviewOpen(false);
      setNotices((all) => ({ ...all, [draft.email]: { text: `Membership saved for ${draft.email}. Their previous sessions have ended; they sign in with the new access.`, ok: true } }));
    } catch (cause) { setMembershipError(`${(cause as Error).message} Refresh before trying again if the result is uncertain.`); }
    finally { setMembershipSaving(false); await load(); }
  }
  function confirmReview() { const action = review; setReview(null); action?.commit(); }

  const role = (person: Person) => person.disabled ? { lamp: "lamp-off", text: "Disabled" } : active(person).length === 0 ? { lamp: "lamp-wait", text: "Invited, not joined" } : { lamp: "lamp-on", text: "Signed up" };

  return <div className="grid content-start gap-6">
    <header className="flex flex-wrap items-end justify-between gap-4"><div><p className="plate">Access</p><h1 tabIndex={-1} className="mt-1 text-2xl font-semibold tracking-tight">People</h1><p className="text-muted-foreground mt-1 text-sm">Who can sign in to your applications, the groups they belong to, and who runs this console.</p><PeopleTabs canAdministrators={!!session?.tools.includes("administrators")} /></div><div className="flex flex-col items-start gap-2 sm:items-end"><div className="flex flex-wrap gap-2"><Button variant="outline" size="sm" onClick={() => void load()} disabled={loading}><RefreshCw className={`size-3.5 ${loading ? "animate-spin" : ""}`} aria-hidden="true" />Refresh</Button>{people?.enabled !== false && <Button size="sm" onClick={startInvite}><UserPlus className="size-3.5" aria-hidden="true" />Invite person</Button>}</div><p className="text-muted-foreground inline-flex flex-wrap items-center gap-x-2 gap-y-1 font-mono text-[0.7rem]" title={error || undefined}><span className={`lamp ${freshnessLamp}`} aria-hidden="true" /><span>{freshness} · {age}</span>{error && <span className="text-destructive">· {error}</span>}</p></div></header>
    {people && !people.enabled ? <section className="panel" aria-label="People"><div className="panel-head"><h2 className="text-sm font-medium">Passkeys are not enabled on this edge</h2></div><div className="p-5"><p className="text-muted-foreground text-sm">Add <code className="font-mono">auth.passkeys</code> (<code className="font-mono">rp_id</code>, <code className="font-mono">login_url</code>) and start Ozal with <code className="font-mono">-db</code>.</p></div></section> : <>
      <dl className="mb-2 flex flex-wrap gap-x-8 gap-y-3" aria-label="People summary">{[[people?.users.length ?? "—", "people", false], [people ? activeKeys : "—", "passkeys", false], [people ? disabledCount : "—", "disabled", disabledCount > 0]] .map(([value, label, warning]) => <div key={String(label)} className="flex items-baseline gap-2"><dt className="sr-only">{String(label)}</dt><dd className={`text-xl font-medium tabular-nums ${warning ? "text-warning" : ""}`}>{value}</dd><span className="text-muted-foreground text-sm" aria-hidden="true">{label}</span></div>)}</dl>
      {error && people && <p className="border-warning/40 bg-warning/[0.06] -mt-4 rounded-md border p-3 text-sm" role="status">Showing the last successful read. Refresh failed: {error}</p>}
      {!people ? error ? <p className="text-destructive text-sm" role="alert">People could not be read ({error}). <button className="underline" onClick={() => void load()}>Retry</button></p> : <div className="space-y-3" aria-busy="true" aria-label="Loading people"><div className="bg-muted h-14 animate-pulse rounded-lg" /><div className="bg-muted h-14 animate-pulse rounded-lg" /></div> : people.users.length === 0 ? <div className="panel px-5 py-10 text-center"><p className="font-medium">Nobody invited yet</p><p className="text-muted-foreground mt-1 text-sm">An invitation is a one-time link; they create a passkey on their own device.</p><Button className="mt-4" onClick={startInvite}><UserPlus className="size-4" aria-hidden="true" />Invite person</Button></div> : <ul className="bg-card divide-y rounded-lg" aria-label="People">{people.users.map((person) => {
        const status = role(person); const pending = pendingForPerson(person.email); const notice = notices[person.email];
        return <li key={person.email} className="grid gap-x-6 gap-y-2 px-4 py-3 sm:px-5 md:grid-cols-[minmax(0,1.2fr)_minmax(0,1fr)_16rem] md:items-center"><div className="min-w-0"><p className="truncate text-sm font-medium">{person.name || person.email}</p><p className="text-muted-foreground flex items-center gap-1.5 truncate text-xs"><span className={`lamp ${status.lamp}`} aria-hidden="true" />{status.text}{person.name && <> <span aria-hidden="true">·</span> {person.email}</>}</p></div><ul className="flex min-w-0 flex-wrap gap-1.5" aria-label={`Groups of ${person.email}`}>{person.groups.length ? person.groups.map((group) => <li key={group} className="chip">{group}</li>) : <li className="text-muted-foreground text-xs">No groups</li>}</ul><div className="flex flex-wrap items-center gap-1 md:justify-end"><Button variant="ghost" size="sm" aria-label={`Edit groups for ${person.email}`} disabled={busyForPerson(person.email)} onClick={() => reviewMembership(person)}>Groups</Button><Button variant="ghost" size="sm" aria-label={`Passkeys of ${person.email}`} onClick={() => { setKeysFor(person.email); setKeysOpen(true); }}>Passkeys · {active(person).length}</Button><Button variant="ghost" size="sm" className={person.disabled ? "" : "text-destructive"} disabled={busyForPerson(person.email)} onClick={() => reviewToggle(person)}>{person.disabled ? "Enable" : "Disable"}</Button></div>{pending && <p className="text-muted-foreground text-xs md:col-span-3" role="status">Waiting for the server… <button className="underline" onClick={() => stopMutation(pending)}>Stop waiting</button></p>}{notice && <p className={`text-sm md:col-span-3 ${notice.ok ? "" : "text-destructive"}`} role="status">{notice.text}</p>}</li>;
      })}</ul>}
    </>}

    <Modal open={inviteOpen} onOpenChange={setInviteOpen} title={created ? "Invitation ready" : "Invite person"} intro={created ? "" : "They get a one-time link to create a passkey on their own device. It works once and expires in 15 minutes."}>{created ? <div role="status"><p className="text-sm font-semibold">Invitation for {created.email} · expires at {new Date(created.expires_at).toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" })}</p><p className="text-muted-foreground mb-3 text-xs">Send it over a channel you trust: whoever opens it first gets the passkey. It is shown only now.</p><div className="flex flex-wrap gap-2"><Input ref={invitationInput} readOnly aria-label="Invitation link" value={created.link} className="min-w-0 flex-1 font-mono text-xs" onFocus={(event) => event.currentTarget.select()} /><Button variant="outline" onClick={() => void copyInvitation()}><Copy className="size-4" aria-hidden="true" />{copied ? "Copied" : "Copy link"}</Button>{typeof navigator !== "undefined" && "share" in navigator && <Button variant="outline" onClick={() => void shareInvitation()}><Share2 className="size-4" aria-hidden="true" />Share</Button>}</div>{copyError && <p className="text-destructive mt-2 text-sm" role="status">{copyError}</p>}<div className="mt-5 flex justify-end gap-2"><Button variant="ghost" onClick={startInvite}>Invite another</Button><Button onClick={() => setInviteOpen(false)}>Done</Button></div></div> : <form className="grid gap-4" onSubmit={(event) => void invite(event)}><div className="grid gap-2"><label htmlFor="inv-email" className="text-sm font-medium">Email</label><Input id="inv-email" type="email" required value={email} onChange={(event) => setEmail(event.target.value)} placeholder="ana@example.com" autoComplete="off" /></div><div className="grid gap-2"><label htmlFor="inv-name" className="text-sm font-medium">Name (optional)</label><Input id="inv-name" value={name} onChange={(event) => setName(event.target.value)} placeholder="Ana" autoComplete="off" /></div><div className="grid gap-2"><label htmlFor="inv-groups" className="text-sm font-medium">Groups (comma-separated)</label><Input id="inv-groups" value={groups} onChange={(event) => setGroups(event.target.value)} placeholder="ops, staff" autoComplete="off" />{known.length > 0 && <p className="text-muted-foreground text-xs">Existing: {known.join(", ")}</p>}</div><div className="grid gap-2"><label htmlFor="inv-dest" className="text-sm font-medium">After the passkey, open</label><select id="inv-dest" value={destination} onChange={(event) => setDestination(event.target.value)} className="border-input bg-background h-9 rounded-md border px-3 text-sm"><option value="">Their apps page</option>{(people?.protected_hosts ?? []).map((host) => <option key={host} value={host}>{host}</option>)}</select></div><p className="text-muted-foreground text-xs">Someone who lost every passkey is recovered on the host: <code className="font-mono">ozal user invite -recover</code>.</p>{inviteError && <p className="text-destructive text-sm" role="alert">{inviteError}</p>}<div className="flex justify-end gap-2">{inviting && <Button type="button" variant="outline" onClick={stopInvite}>Stop waiting</Button>}<Button type="button" variant="ghost" onClick={() => setInviteOpen(false)}>Cancel</Button><Button type="submit" disabled={inviting}>{inviting ? "Creating…" : "Create invitation"}</Button></div></form>}</Modal>

    <Modal open={keysOpen} onOpenChange={setKeysOpen} title="Passkeys" intro={keysPerson?.email ?? ""}>{keysPerson && <>{keysPerson.passkeys.length === 0 ? <p className="text-muted-foreground text-sm">They have not used the invitation yet. Invite them again if the link expired.</p> : <ul className="divide-y rounded-lg border">{keysPerson.passkeys.map((key) => <li key={key.id} className={`flex flex-wrap items-center justify-between gap-3 px-4 py-3 ${key.revoked ? "opacity-55" : ""}`}><div className="min-w-0"><p className="text-sm font-medium">{key.provider} <span className="text-muted-foreground ml-1 text-xs font-normal">{key.synced ? "synced" : "device only"}</span></p><p className="text-muted-foreground text-xs">added {day(key.created)} · last used {day(key.last_used)}{key.revoked ? ` · revoked ${day(key.revoked)}` : ""}</p></div>{!key.revoked && <Button variant="outline" size="sm" className="text-destructive" disabled={busyForPerson(keysPerson.email)} onClick={() => reviewRevoke(keysPerson.email, key.id.slice(0, 16), key.provider)}>Revoke</Button>}</li>)}</ul>}{notices[keysPerson.email] && <p className={`mt-3 text-sm ${notices[keysPerson.email].ok ? "" : "text-destructive"}`} role="status">{notices[keysPerson.email].text}</p>}<div className="mt-5 flex justify-end"><Button variant="ghost" onClick={() => setKeysOpen(false)}>Close</Button></div></>}</Modal>

    <Modal open={membershipDraftOpen && !!membershipDraft} onOpenChange={(open) => { if (!open && !membershipSaving) { setMembershipDraftOpen(false); setMembershipDraft(null); setMembershipError(""); } }} title="Edit groups" intro={membershipDraft?.email ?? ""}>{membershipDraft && <form className="grid gap-4" onSubmit={prepareMembership}><div><label htmlFor="person-groups" className="mb-2 block text-sm font-medium">Groups, separated by commas</label><Input id="person-groups" value={membershipDraft.value} onChange={(event) => setMembershipDraft((draft) => draft ? { ...draft, value: event.target.value } : draft)} disabled={membershipSaving} maxLength={8192} placeholder="ops, staff" autoComplete="off" />{membershipChosen.length > 0 && <ul className="mt-3 flex flex-wrap gap-1.5" aria-label="Groups after this change">{membershipChosen.map((group) => <li key={group}><button type="button" className="chip" aria-label={`Remove ${group}`} onClick={() => setMembershipDraft((draft) => draft ? { ...draft, value: parseGroupNames(draft.value).filter((name) => name !== group).join(", ") } : draft)}>{group} <span aria-hidden="true">×</span></button></li>)}</ul>}</div>{suggestions.length > 0 && <div><p className="text-muted-foreground mb-2 text-xs">Existing groups</p><ul className="flex flex-wrap gap-1.5">{suggestions.map((group) => <li key={group}><button type="button" className="chip chip-add" aria-label={`Add ${group}`} onClick={() => setMembershipDraft((draft) => draft ? { ...draft, value: [...parseGroupNames(draft.value), group].join(", ") } : draft)}>+ {group}</button></li>)}</ul></div>}<p className="text-muted-foreground text-xs">Names are case-sensitive. An app that requires several groups needs all of them. OIDC memberships stay with their provider.</p>{membershipError && <p className="text-destructive text-sm" role="alert">{membershipError}</p>}<div className="flex justify-end gap-2"><Button type="button" variant="ghost" disabled={membershipSaving} onClick={() => { setMembershipDraftOpen(false); setMembershipDraft(null); }}>Cancel</Button><Button type="submit" disabled={membershipSaving}>{membershipSaving ? "Saving…" : "Review change"}</Button></div></form>}</Modal>
    <Modal open={membershipReviewOpen && !!membershipDraft} onOpenChange={(open) => { if (!open && !membershipSaving) { setMembershipReviewOpen(false); setMembershipDraft(null); } }} title="Change group membership?" intro="Review change"><dl className="border-border divide-border divide-y rounded-lg border text-sm"><ReviewRow label="Target" value={membershipDraft?.email ?? ""} /><ReviewRow label="Current" value={membershipDraft?.expected.join(", ") || "No local groups"} /><ReviewRow label="Requested" value={membershipDraft ? parseGroupNames(membershipDraft.value).join(", ") || "No local groups" : ""} /><ReviewRow label="Consequence" value="This changes which applications the person may open. Their existing native sessions will end immediately." /><ReviewRow label="Details" value="If another operator changes membership before you save, this update is refused. New memberships are evaluated by each app’s policy." /></dl>{membershipError && <p className="text-destructive mt-3 text-sm" role="alert">{membershipError}</p>}<div className="mt-5 flex justify-end gap-2"><Button variant="outline" disabled={membershipSaving} onClick={() => { setMembershipReviewOpen(false); setMembershipDraftOpen(true); }}>Back</Button><Button disabled={membershipSaving} onClick={() => void saveMembership()}>{membershipSaving ? "Saving…" : "Save membership"}</Button></div></Modal>
    <Modal open={!!review} onOpenChange={(open) => { if (!open) setReview(null); }} title={review?.title ?? "Review change"} intro="Review change"><dl className="border-border divide-border divide-y rounded-lg border text-sm"><ReviewRow label="Target" value={review?.target ?? ""} /><ReviewRow label="Current" value={review?.current ?? ""} /><ReviewRow label="Requested" value={review?.requested ?? ""} /><ReviewRow label="Consequence" value={review?.consequence ?? ""} /><ReviewRow label="Details" value={review?.detail ?? ""} /></dl><div className="mt-5 flex justify-end gap-2"><Button variant="outline" onClick={() => setReview(null)}>Cancel</Button><Button variant={review?.label === "Disable person" || review?.label === "Revoke passkey" ? "destructive" : "default"} onClick={confirmReview}>{review?.label ?? "Confirm"}</Button></div></Modal>
  </div>;
}

function PeopleTabs({ canAdministrators }: { canAdministrators: boolean }) {
  return <nav className="console-tabs" aria-label="People views"><Link aria-current="page" className="console-tab" to="/people">People</Link><Link className="console-tab" to="/groups">Groups</Link>{canAdministrators && <Link className="console-tab" to="/administrators">Administrators</Link>}</nav>;
}
function ReviewRow({ label, value }: { label: string; value: string }) { return <div className="grid gap-1 px-3 py-2 sm:grid-cols-[7rem_1fr]"><dt className="text-muted-foreground">{label}</dt><dd className="break-words">{value}</dd></div>; }
