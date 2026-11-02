import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent } from "react";
import { Link, useNavigate } from "react-router";
import { Plus, RefreshCw } from "lucide-react";
import { fetchGroups, fetchPeople, setGroups as savePersonGroups, type Group, type Person } from "$lib/api";
import { ageLabel, elapsed, isStale } from "$lib/freshness";
import { groupMemberChanges, membershipAfterChange, type GroupMemberChange } from "$lib/groupChanges";
import { Button } from "$lib/components/ui/button/react";
import { Input } from "$lib/components/ui/input/react";
import { Modal } from "./Modal";
import { useSession } from "./session";

type GroupDraft = { name: string; picked: string[]; current: string[]; changes: GroupMemberChange[]; creating: boolean };

export function Groups() {
  const { session } = useSession();
  const navigate = useNavigate();
  const token = session?.token ?? "";
  const [groups, setGroups] = useState<Group[] | null>(null);
  const [people, setPeople] = useState<Person[]>([]);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [observedAt, setObservedAt] = useState<number | null>(null);
  const [editOpen, setEditOpen] = useState(false);
  const [creating, setCreating] = useState(false);
  const [groupName, setGroupName] = useState("");
  const [picked, setPicked] = useState<string[]>([]);
  const [formError, setFormError] = useState("");
  const [draft, setDraft] = useState<GroupDraft | null>(null);
  const [saving, setSaving] = useState(false);
  const [notice, setNotice] = useState<{ text: string; ok: boolean } | null>(null);
  const read = useRef<AbortController | null>(null);
  const requestID = useRef(0);

  const load = useCallback(async () => {
    if (!token) return;
    read.current?.abort();
    const controller = new AbortController();
    read.current = controller;
    const id = ++requestID.current;
    setLoading(true);
    try {
      const [catalog, directory] = await Promise.all([fetchGroups(token, controller.signal), fetchPeople(controller.signal)]);
      if (controller.signal.aborted || requestID.current !== id) return;
      setGroups(catalog); setPeople(directory.users); setError(""); setObservedAt(Date.now());
    } catch (cause) {
      if (!controller.signal.aborted && requestID.current === id) setError((cause as Error).message);
    } finally {
      if (requestID.current === id) { setLoading(false); read.current = null; }
    }
  }, [token]);
  useEffect(() => {
    setGroups(null); setPeople([]); setError(""); setObservedAt(null);
    void load();
    return () => { requestID.current++; read.current?.abort(); read.current = null; };
  }, [load]);
  useEffect(() => {
    if (!token || editOpen || !!draft || saving) return;
    const timer = window.setInterval(() => void load(), 15_000);
    return () => window.clearInterval(timer);
  }, [token, editOpen, draft, saving, load]);

  const name = groupName.trim();
  const current = useMemo(() => groups?.find((group) => group.name === name)?.members ?? [], [groups, name]);
  const changes = groupMemberChanges(current, picked);
  const unused = groups?.filter((group) => group.members.length === 0 && group.hosts.length > 0) ?? [];
  const age = observedAt === null ? "not loaded" : ageLabel(elapsed(Date.now(), observedAt));
  const stale = observedAt !== null && isStale(elapsed(Date.now(), observedAt), Boolean(error));
  const freshness = observedAt === null ? "Not loaded" : error ? "Last read failed" : loading ? "Refreshing" : stale ? "Stale" : "Current";
  const freshnessLamp = observedAt === null ? "lamp-idle" : error || stale ? "lamp-off" : loading ? "lamp-wait" : "lamp-on";
  const label = (email: string) => { const person = people.find((entry) => entry.email === email); return person?.name ? `${person.name} (${email})` : email; };

  function edit(group?: Group) {
    setCreating(!group); setGroupName(group?.name ?? ""); setPicked([...(group?.members ?? [])]);
    setFormError(""); setNotice(null); setEditOpen(true);
  }
  function prepare(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); setFormError("");
    const cleanName = groupName.trim();
    if (!cleanName || cleanName.includes(",")) { setFormError("Give the group a name without commas."); return; }
    if (creating && groups?.some((group) => group.name === cleanName && group.members.length)) { setFormError(`${cleanName} already exists; edit it from the list.`); return; }
    const existing = groups?.find((group) => group.name === cleanName)?.members ?? [];
    const planned = groupMemberChanges(existing, picked);
    if (!planned.length) { setFormError(creating ? "Choose at least one member: a group exists through its members or an application that requires it." : "Nothing changed."); return; }
    setDraft({ name: cleanName, picked: [...picked], current: [...existing], changes: planned, creating });
    setEditOpen(false);
  }
  async function save() {
    if (!draft) return;
    const reviewed = draft;
    setSaving(true);
    const failed: string[] = [];
    for (const change of reviewed.changes) {
      const person = people.find((entry) => entry.email === change.email);
      if (!person) { failed.push(`${change.email}: no longer listed`); continue; }
      const next = membershipAfterChange(person.groups, reviewed.name, change.join);
      try { await savePersonGroups(token.trim(), person.email, person.groups, next); }
      catch (cause) { failed.push(`${person.email}: ${(cause as Error).message}`); }
    }
    setSaving(false); setDraft(null);
    const saved = reviewed.changes.length - failed.length;
    setNotice(failed.length
      ? { text: `${saved} of ${reviewed.changes.length} changes saved for ${reviewed.name}. Not saved: ${failed.join("; ")}. Refresh and review before retrying.`, ok: false }
      : { text: `${reviewed.name} saved. The sessions of the ${reviewed.changes.length} affected ${reviewed.changes.length === 1 ? "person" : "people"} ended; they sign in with the new access.`, ok: true });
    await load();
  }

  const added = draft?.changes.filter((change) => change.join).map((change) => change.email) ?? [];
  const removed = draft?.changes.filter((change) => !change.join).map((change) => change.email) ?? [];
  const requested = [added.length ? `Add ${added.map(label).join(", ")}` : "", removed.length ? `Remove ${removed.map(label).join(", ")}` : ""].filter(Boolean).join(" · ");
  const consequence = draft ? `This changes which applications ${draft.changes.length === 1 ? "this person" : "these people"} may open. Their existing native sessions end immediately.` : "";

  return <div className="grid content-start gap-6">
    <header className="flex flex-wrap items-end justify-between gap-4"><div><p className="plate">Access</p><h1 tabIndex={-1} className="mt-1 text-2xl font-semibold tracking-tight">People</h1><p className="text-muted-foreground mt-1 text-sm">Who can sign in to your applications, the groups they belong to, and who runs this console.</p><PeopleTabs canAdministrators={!!session?.tools.includes("administrators")} /></div><div className="flex flex-col items-start gap-2 sm:items-end"><div className="flex flex-wrap gap-2"><Button variant="outline" size="sm" onClick={() => void load()} disabled={loading}><RefreshCw className={`size-3.5 ${loading ? "animate-spin" : ""}`} aria-hidden="true" />Refresh</Button><Button size="sm" onClick={() => edit()} disabled={!people.length}><Plus className="size-3.5" aria-hidden="true" />New group</Button></div><p className="text-muted-foreground inline-flex flex-wrap items-center gap-x-2 gap-y-1 font-mono text-[0.7rem]" title={error || undefined}><span className={`lamp ${freshnessLamp}`} aria-hidden="true" /><span>{freshness} · {age}</span>{error && <span className="text-destructive">· {error}</span>}</p></div></header>
    <dl className="mb-2 flex flex-wrap gap-x-8 gap-y-3" aria-label="Groups summary">{[[groups?.length ?? "—", "groups", false], [groups?.filter((group) => group.hosts.length).length ?? "—", "required by an app", false], [groups ? unused.length : "—", "required with no members", unused.length > 0]] .map(([value, label, warning]) => <div key={String(label)} className="flex items-baseline gap-2"><dt className="sr-only">{String(label)}</dt><dd className={`text-xl font-medium tabular-nums ${warning ? "text-warning" : ""}`}>{value}</dd><span className="text-muted-foreground text-sm" aria-hidden="true">{label}</span></div>)}</dl>
    {notice && <p className={`-mt-4 text-sm ${notice.ok ? "" : "text-destructive"}`} role="status">{notice.text}</p>}
    {error && groups && <p className="border-warning/40 bg-warning/[0.06] -mt-4 rounded-md border p-3 text-sm" role="status">Showing the last successful read. Refresh failed: {error}</p>}
    {!groups ? error ? <p className="text-destructive text-sm" role="alert">Groups could not be read ({error}). <button className="underline" onClick={() => void load()}>Retry</button></p> : <div className="space-y-3" aria-busy="true" aria-label="Loading groups"><div className="bg-muted h-14 animate-pulse rounded-lg" /><div className="bg-muted h-14 animate-pulse rounded-lg" /></div> : groups.length === 0 ? <div className="panel px-5 py-10 text-center"><p className="font-medium">No groups yet</p><p className="text-muted-foreground mx-auto mt-1 max-w-md text-sm">Put people in a group, then require it on an application so only they can open it.</p>{people.length ? <Button className="mt-4" onClick={() => edit()}><Plus className="size-4" />New group</Button> : <Button className="mt-4" variant="outline" onClick={() => navigate("/people")}>Invite someone first</Button>}</div> : <><ul className="bg-card divide-y rounded-lg" aria-label="Groups">{groups.map((group) => <li key={group.name} className="grid gap-x-6 gap-y-2 px-4 py-3 sm:px-5 md:grid-cols-[minmax(8rem,12rem)_1fr_1fr_8rem] md:items-center"><h2 className="font-mono text-sm font-medium break-all">{group.name}</h2><div className="min-w-0"><p className="text-muted-foreground mb-1 text-xs md:sr-only">Members</p>{group.members.length ? <ul className="flex flex-wrap gap-1.5" aria-label={`Members of ${group.name}`}>{group.members.map((email) => <li key={email} className="chip">{email}</li>)}</ul> : <p className="text-warning text-xs">No members{group.hosts.length ? ": nobody passes this requirement" : ""}</p>}</div><div className="min-w-0"><p className="text-muted-foreground mb-1 text-xs md:sr-only">Required by</p>{group.hosts.length ? <ul className="flex flex-wrap gap-1.5" aria-label={`Applications requiring ${group.name}`}>{group.hosts.map((host) => <li key={host} className="text-muted-foreground font-mono text-xs break-all">{host}</li>)}</ul> : <p className="text-muted-foreground text-xs">No application requires it</p>}</div><div className="md:text-right"><Button variant="ghost" size="sm" aria-label={`Edit members of ${group.name}`} onClick={() => edit(group)}>Edit members</Button></div></li>)}</ul><p className="text-muted-foreground -mt-3 text-xs">An application that requires several groups lets in only people in all of them. Groups from an OIDC provider stay with that provider and are not listed here.</p></>}

    <Modal open={editOpen} onOpenChange={setEditOpen} title={creating ? "New group" : `Members of ${groupName}`} intro={creating ? "Name it and choose who belongs to it. Require it in Applications to restrict an app to them." : "Each changed person’s sessions end so the new access applies at their next sign-in."}><form className="grid gap-4" onSubmit={prepare}>{creating && <div className="grid gap-2"><label htmlFor="group-name" className="text-sm font-medium">Name</label><Input id="group-name" value={groupName} onChange={(event) => setGroupName(event.target.value)} maxLength={64} placeholder="ops" autoComplete="off" /></div>}<fieldset><legend className="mb-2 text-sm font-medium">Members <span className="text-muted-foreground font-normal">· {picked.length} of {people.length}</span></legend><div className="max-h-72 divide-y overflow-y-auto rounded-lg border">{people.map((person) => <label key={person.email} className="hover:bg-accent/40 flex cursor-pointer items-center gap-3 px-3 py-2 text-sm"><input type="checkbox" checked={picked.includes(person.email)} onChange={(event) => setPicked((all) => event.target.checked ? [...all, person.email] : all.filter((email) => email !== person.email))} className="mt-1 accent-[var(--primary)]" /><span className="min-w-0 flex-1 truncate">{person.name || person.email}{person.name && <span className="text-muted-foreground ml-1.5 text-xs">{person.email}</span>}</span>{person.disabled && <span className="text-muted-foreground text-xs">disabled</span>}</label>)}</div></fieldset>{formError && <p className="text-destructive text-sm" role="alert">{formError}</p>}<div className="flex justify-end gap-2"><Button type="button" variant="ghost" onClick={() => setEditOpen(false)}>Cancel</Button><Button type="submit" disabled={saving}>{saving ? "Saving…" : "Review change"}</Button></div></form></Modal>
    <Modal open={!!draft} onOpenChange={(open) => { if (!open && !saving) setDraft(null); }} title={draft?.creating ? "Create this group?" : "Change group members?"} intro="Review change"><dl className="border-border divide-border divide-y rounded-lg border text-sm"><ReviewRow label="Target" value={draft?.name ?? ""} /><ReviewRow label="Current" value={draft?.current.length ? draft.current.map(label).join(", ") : "No members"} /><ReviewRow label="Requested" value={requested} /><ReviewRow label="Consequence" value={consequence} /><ReviewRow label="Details" value="Each person's update uses the membership read here as its expected value. If it changed meanwhile, that person's update is refused and other people can still be saved." /></dl><div className="mt-5 flex justify-end gap-2"><Button variant="outline" disabled={saving} onClick={() => setDraft(null)}>Cancel</Button><Button disabled={saving} onClick={() => void save()}>{saving ? "Saving…" : "Save group"}</Button></div></Modal>
  </div>;
}

function PeopleTabs({ canAdministrators }: { canAdministrators: boolean }) {
  return <nav className="console-tabs" aria-label="People views"><Link className="console-tab" to="/people">People</Link><Link aria-current="page" className="console-tab" to="/groups">Groups</Link>{canAdministrators && <Link className="console-tab" to="/administrators">Administrators</Link>}</nav>;
}

function ReviewRow({ label, value }: { label: string; value: string }) { return <div className="grid gap-1 px-3 py-2 sm:grid-cols-[7rem_1fr]"><dt className="text-muted-foreground">{label}</dt><dd className="break-words">{value}</dd></div>; }
