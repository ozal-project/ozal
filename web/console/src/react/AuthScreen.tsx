import { useEffect, useId, useState, type FormEvent } from "react";
import { Button } from "$lib/components/ui/button/react";
import { Input } from "$lib/components/ui/input/react";
import { Label } from "$lib/components/ui/label/react";
import { useSession } from "./session";
import { adminState, enrolInfo, enrolWithPasskey, explainPasskeyError, signInWithPasskey, type EnrolInfo } from "$lib/passkey";
import { choices, type Choice } from "$lib/passkeyChoices";
import { hostLoginCommand } from "$lib/hostCommand";
import { startLoginHost } from "$lib/loginHost";

const toolList = [
  ["administrators", "Administrators", "Invite and disable other administrators (up to your own tools)"],
  ["applications", "Applications", "Add, change and remove applications and routes"],
  ["people", "People", "Invite people, edit groups, disable accounts and passkeys"],
  ["sites", "Sites", "Publish and roll back static sites"],
  ["updates", "Updates", "See when a signed release is available"],
  ["updates.install", "Install updates", "Install a reviewed signed release after confirming with a passkey"],
  ["publishing", "CI publishing", "Choose which repositories may publish from CI"],
  ["connectors", "Connectors", "See private hosts (enrolment stays on the host)"],
] as const;

function CopyText({ value, label }: { value: string; label: string }) {
  const [copied, setCopied] = useState(false);
  return <Button type="button" variant="outline" size="sm" onClick={() => void navigator.clipboard.writeText(value).then(() => { setCopied(true); setTimeout(() => setCopied(false), 1500); })}>{copied ? "Copied" : label}</Button>;
}

export function AuthScreen() {
  const { start, notice, enrol, setEnrol } = useSession();
  const [mode, setMode] = useState<"passkey" | "host">("passkey");
  const [enrolled, setEnrolled] = useState<boolean | null | undefined>();
  const [enrolCommand, setEnrolCommand] = useState("sudo ozal console enrol");
  const [loginHost, setLoginHost] = useState("");
  const [waiting, setWaiting] = useState(false);
  const [error, setError] = useState("");
  const [stuck, setStuck] = useState(false);
  const [pasted, setPasted] = useState("");
  const [pasteError, setPasteError] = useState("");
  const localHost = ["ozal.localhost", "switchboard.localhost", "localhost"].includes(location.hostname);
  const id = useId();

  useEffect(() => {
    const controller = new AbortController();
    void adminState(controller.signal).then((state) => {
      if (controller.signal.aborted) return;
      setEnrolled(state ? state.enrolled : null);
      if (state?.enrol_command) setEnrolCommand(state.enrol_command);
      if (state?.login_host) setLoginHost(state.login_host);
      if (!state) setMode("host");
    }).catch(() => { if (!controller.signal.aborted) { setEnrolled(null); setMode("host"); } });
    return () => controller.abort();
  }, []);

  const passkey = async (hints?: string[], here = false) => {
    if (waiting) return;
    setWaiting(true); setError(""); setStuck(false);
    try {
      if (loginHost && !here) { location.assign(await startLoginHost({ hints })); return; }
      start(await signInWithPasskey(hints), true);
    } catch (cause) { setError(explainPasskeyError(cause, "sign-in")); setStuck((cause as Error)?.name === "NotAllowedError"); }
    finally { setWaiting(false); }
  };

  const submitPasted = (event: FormEvent) => {
    event.preventDefault();
    const text = pasted.trim();
    const token = text.includes("enrol=") ? new URLSearchParams(text.slice(text.indexOf("#") + 1)).get("enrol") ?? "" : text;
    if (!/^[A-Za-z0-9_-]{20,128}$/.test(token)) { setPasteError("That does not look like a setup link."); return; }
    setPasted(""); setPasteError(""); setEnrol(token);
  };

  return <main className="auth-shell"><div className="auth-column w-full max-w-md">
    <header className="auth-mark"><div className="grid size-[72px] place-items-center rounded-2xl border border-border bg-card text-2xl font-semibold tracking-tight">O</div><div><p className="auth-name">ozal</p><p className="auth-sub">Private console</p></div></header>
    <section className="auth-card" aria-labelledby="auth-title">
      <h1 id="auth-title" className="text-lg font-semibold">{enrol ? "Create your passkey" : mode === "host" ? "Host login" : enrolled === false ? "Set up the first administrator" : "Sign in"}</h1>
      {enrol ? <EnrolPanel token={enrol} loginHost={loginHost} /> : mode === "host" ? <HostPanel /> : enrolled === false ? <>
        <p className="text-muted-foreground mt-1 mb-4 text-sm">Paste the setup link the installer printed.</p>
        <form className="flex items-center gap-2" onSubmit={submitPasted}>
          <Label htmlFor={`${id}-setup`} className="sr-only">Setup link</Label><Input id={`${id}-setup`} value={pasted} onChange={(event) => setPasted(event.target.value)} autoComplete="off" spellCheck={false} placeholder="http://ozal.localhost:…#enrol=…" />
          <Button type="submit" size="sm" disabled={!pasted.trim()}>Continue</Button>
        </form>
        {pasteError && <p className="text-destructive mt-2 text-xs" role="alert">{pasteError}</p>}
        <p className="text-muted-foreground mt-4 text-xs leading-relaxed">The administrator account needs a passkey. If this computer cannot create one yet, a host login lets you in for a while without creating the account.</p>
        <details className="mt-5 text-sm"><summary className="text-muted-foreground hover:text-foreground cursor-pointer">Lost the link?</summary><p className="text-muted-foreground mt-2 mb-2 text-xs">Run this on the server for a new one:</p><code className="block break-all text-xs">{enrolCommand}</code><CopyText value={enrolCommand} label="Copy enrol command" /></details>
      </> : enrolled ? <>
        {notice && !error ? <p className="text-muted-foreground mt-1 text-sm" role="status">{notice}</p> : <p className="text-muted-foreground mt-1 text-sm">Welcome back.</p>}
        {error && <p className="text-destructive mt-3 text-sm" role="alert">{error}</p>}
        <Button className="mt-6 w-full" onClick={() => void passkey()} disabled={waiting || !localHost}>{waiting ? "Waiting for your device…" : "Sign in with passkey"}</Button>
        {stuck && <div className="mt-3 flex flex-wrap gap-2">{(Object.keys(choices) as Choice[]).map((choice) => <Button key={choice} variant="outline" size="sm" onClick={() => void passkey(choices[choice].hints)}>{choices[choice].label}</Button>)}</div>}
        {loginHost && <p className="text-muted-foreground mt-3 text-xs leading-relaxed">You confirm it's you on <span className="text-foreground">{new URL(loginHost).host}</span>, this edge's sign-in address, where every kind of passkey works, and come back here. <button type="button" className="underline underline-offset-4" disabled={waiting || !localHost} onClick={() => void passkey(undefined, true)}>Use a passkey made for this console's own address</button></p>}
      </> : <p className="text-muted-foreground mt-2 text-sm" role="status">Checking…</p>}
      {!localHost && !enrol && mode === "passkey" && enrolled !== undefined && <p className="border-warning/40 bg-warning/[0.06] mt-4 rounded-md border px-3 py-2 text-xs" role="status">Open this console as <span className="font-mono">ozal.localhost:{location.port || "9091"}</span>: passkeys do not work on an IP address.</p>}
    </section>
    {!enrol && enrolled !== undefined && <div className="auth-links text-xs">{mode === "host" ? enrolled !== null && <button className="text-muted-foreground hover:text-foreground underline-offset-4 hover:underline" onClick={() => setMode("passkey")}>{enrolled === false ? "Back to setup" : "Sign in with passkey"}</button> : <><button className="text-muted-foreground hover:text-foreground underline-offset-4 hover:underline" onClick={() => setMode("host")}>{enrolled === false ? "Use a host login instead" : "Use a host login"}</button></>}</div>}
  </div></main>;
}

function HostPanel() {
  const { start, notice } = useSession();
  const [picked, setPicked] = useState<string[]>(toolList.map(([id]) => id));
  const [draft, setDraft] = useState("");
  const [error, setError] = useState("");
  const [checking, setChecking] = useState(false);
  const allTools = picked.length === toolList.length;
  const command = hostLoginCommand(location.origin, allTools ? undefined : picked);
  const unlock = async (event: FormEvent) => {
    event.preventDefault();
    const token = draft.trim();
    if (!token || checking) return;
    setChecking(true); setError("");
    try {
      const response = await fetch("/_/api/operators/session", { headers: { Authorization: `Bearer ${token}` }, cache: "no-store", signal: AbortSignal.timeout(10_000) });
      if (!response.ok) throw new Error("This credential could not be verified. Check it and try again.");
      const value: { kind: string; name?: string; expires?: string; tools: string[] } = await response.json();
      start({ token, name: value.name || (value.kind === "sites" ? "Sites token" : ""), expires: value.expires, tools: value.tools }, false);
      setDraft("");
    } catch (cause) { setError((cause as Error).message); }
    finally { setChecking(false); }
  };
  return <>
    <p className="text-muted-foreground mt-1 mb-5 text-sm">Paste the one-time login printed on the server. It lasts 15 minutes.</p>
    {(error || notice) && <p className="text-destructive mb-4 text-sm" role="alert">{error || notice}</p>}
    <fieldset><legend className="plate mb-2">What this login may change</legend><div className="grid grid-cols-2 gap-x-3 gap-y-2">{toolList.map(([id, label, description]) => <label key={id} title={description} className="flex items-center gap-2 text-sm"><input type="checkbox" checked={picked.includes(id)} onChange={(event) => setPicked((current) => event.target.checked ? [...current, id] : current.filter((item) => item !== id))} className="accent-[var(--primary)]" />{label}</label>)}</div></fieldset>
    <p className="text-muted-foreground mt-4 mb-2 text-sm">Run this on the host, replacing YOUR_NAME, and open the link it prints:</p><code className="block break-all text-xs">{command}</code><div className="mt-2"><CopyText value={command} label="Copy login command" /></div>
    <p className="text-muted-foreground mt-2 text-xs">Needs <code className="font-mono">ozal</code> on the host's PATH. If it is not found, use the full path to the binary and add <code className="font-mono">-config /path/to/config.json</code>.</p>
    <form className="mt-5 flex items-center gap-2" onSubmit={(event) => void unlock(event)}><Label className="sr-only" htmlFor="host-admin-token">Admin token</Label><Input id="host-admin-token" type="password" value={draft} onChange={(event) => setDraft(event.target.value)} autoComplete="off" spellCheck={false} placeholder="Or paste the admin token" /><Button type="submit" size="sm" disabled={!draft.trim() || checking}>{checking ? "Checking…" : "Unlock"}</Button></form>
  </>;
}

function EnrolPanel({ token, loginHost }: { token: string; loginHost: string }) {
  const { start } = useSession();
  const [info, setInfo] = useState<EnrolInfo | null>(null);
  const [name, setName] = useState("");
  const [email, setEmail] = useState("");
  const [choice, setChoice] = useState<Choice>("device");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [stuck, setStuck] = useState(false);
  const id = useId();
  useEffect(() => { let live = true; void enrolInfo(token).then((value) => { if (!live) return; setInfo(value); setName(value.name ?? ""); setEmail(value.email ?? ""); }).catch((cause) => { if (live) setError((cause as Error).message); }); return () => { live = false; }; }, [token]);
  const knownEmail = info?.kind === "invite" || info?.kind === "recover";
  const alternative = info?.kind === "device" || info?.kind === "recover";
  const submit = async (event: FormEvent) => {
    event.preventDefault(); if (busy) return;
    setBusy(true); setError(""); setStuck(false);
    try {
      const hints = choices[choice].hints;
      if (loginHost) { location.assign(await startLoginHost({ token, name: name.trim(), email: email.trim(), hints })); return; }
      start(await enrolWithPasskey(token, name.trim(), email.trim(), hints), true);
    } catch (cause) { setError(explainPasskeyError(cause)); setStuck((cause as Error)?.name === "NotAllowedError"); }
    finally { setBusy(false); }
  };
  return <form className="mt-4 grid gap-4" onSubmit={(event) => void submit(event)}>
    {error && <p className="text-destructive text-sm" role="alert">{error}</p>}
    {info?.kind === "device" || info?.kind === "recover" ? <p className="text-sm">{info.kind === "device" ? "This adds a passkey for" : "This link replaces the passkeys of"} <span className="font-medium">{info.name || info.email}</span> ({info.email}).</p> : <>
      <div className="grid gap-1.5"><Label htmlFor={`${id}-name`}>Full name</Label><Input id={`${id}-name`} value={name} onChange={(event) => setName(event.target.value)} required maxLength={80} autoComplete="name" /></div>
      {knownEmail ? <p className="text-muted-foreground text-sm">Invited as <span className="text-foreground font-medium">{info?.email}</span></p> : <div className="grid gap-1.5"><Label htmlFor={`${id}-email`}>Email</Label><Input id={`${id}-email`} value={email} onChange={(event) => setEmail(event.target.value)} required type="email" maxLength={254} autoComplete="email" /><p className="text-muted-foreground text-xs">It names you in the audit log and is how the host recovers your access. No message is sent.</p></div>}
    </>}
    <fieldset><legend className="text-sm font-medium">Where do you keep your passkey?</legend><div className="mt-2 grid gap-2">{(Object.keys(choices) as Choice[]).map((key) => <label key={key} className="flex items-start gap-2 text-sm"><input className="mt-1 accent-[var(--primary)]" type="radio" name={`${id}-choice`} checked={choice === key} onChange={() => setChoice(key)} /><span><span className="font-medium">{choices[key].label}</span><span className="text-muted-foreground block text-xs">{choices[key].detail}</span></span></label>)}</div></fieldset>
    {loginHost && <p className="text-muted-foreground text-xs">You will create it on <span className="text-foreground">{new URL(loginHost).host}</span>, this edge's sign-in address, and come back here.</p>}
    {stuck && <p className="text-muted-foreground text-xs" role="status">If the prompt asked for something you do not have, choose another place above and try again.</p>}
    <Button type="submit" disabled={busy || (!alternative && (!name.trim() || !email.trim()))}>{busy ? "Waiting for your device…" : "Create my passkey"}</Button>
    {!alternative && info?.kind !== "invite" && <details className="text-muted-foreground text-xs" open={stuck}><summary className="cursor-pointer">This computer cannot create a passkey?</summary><p className="mt-2 leading-relaxed">Close the browser's prompt, then run this on the server, replacing YOUR_NAME. It lets you in for a while without creating the administrator account.</p><code>{hostLoginCommand(location.origin)}</code></details>}
  </form>;
}
