import { useEffect, useId, useState, type FormEvent } from "react";
import { Button } from "$lib/components/ui/button/react";
import { Input } from "$lib/components/ui/input/react";
import { Label } from "$lib/components/ui/label/react";
import { useSession } from "./session";
import { adminState, enrolInfo, enrolWithPasskey, explainPasskeyError, signInWithPasskey, type EnrolInfo } from "$lib/passkey";
import { choices, type Choice } from "$lib/passkeyChoices";
import { hostLoginCommand } from "$lib/hostCommand";
import { startLoginHost } from "$lib/loginHost";
import { checkAccessRequest, pollEveryMs, submitAccessRequest, type AccessTicket } from "$lib/accessRequests";
import { Logo } from "./Logo";

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
  const [mode, setMode] = useState<"passkey" | "host" | "request">("passkey");
  const [accessRequests, setAccessRequests] = useState(false);
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
      setAccessRequests(!!state?.access_requests);
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

  const linkClass = "text-muted-foreground hover:text-foreground underline-offset-4 hover:underline";
  const authLinks = () => {
    if (mode === "host") return enrolled !== null && <button className={linkClass} onClick={() => setMode("passkey")}>{enrolled === false ? "Back to setup" : "Back to sign in"}</button>;
    if (mode === "request") return <button className={linkClass} onClick={() => setMode("passkey")}>Back to sign in</button>;
    return <>
      {enrolled && accessRequests && <button className={linkClass} onClick={() => setMode("request")}>New here? Request access</button>}
      <button className={linkClass} onClick={() => setMode("host")}>{enrolled === false ? "Use a host login instead" : "Run the server? Use a host login"}</button>
    </>;
  };

  return <main className="auth-shell"><div className="auth-column w-full max-w-md">
    <header className="auth-mark"><Logo size={72} entrance /><div><p className="auth-name">ozal</p><p className="auth-sub">Private console</p></div></header>
    <section className="auth-card" aria-labelledby="auth-title">
      <h1 id="auth-title" className="text-lg font-semibold">{enrol ? "Create your passkey" : mode === "host" ? "Host login" : mode === "request" ? "Request access" : enrolled === false ? "Set up the first administrator" : "Sign in"}</h1>
      {enrol ? <EnrolPanel token={enrol} loginHost={loginHost} /> : mode === "host" ? <HostPanel /> : mode === "request" && accessRequests ? <RequestPanel /> : enrolled === false ? <>
        {notice && <p className="text-muted-foreground mt-1 mb-4 text-sm" role="status">{notice}</p>}
        {loginHost ? <>
          <p className="text-muted-foreground mt-1 mb-4 text-sm">Nobody has an account on this console yet. The first administrator is created with a code that only someone with access to the server can see, so nobody can get ahead of you.</p>
          <ol className="mb-5 grid gap-4 text-sm">
            <li><p className="font-medium">1. Get a code on the server</p><p className="text-muted-foreground mt-1 text-xs leading-relaxed">Run this in a terminal on the server. It prints a code that works once, for five minutes:</p><code className="mt-2 block break-all text-xs">{enrolCommand}</code><div className="mt-2"><CopyText value={enrolCommand} label="Copy command" /></div></li>
            <li><p className="font-medium">2. Type it on {new URL(loginHost).host}</p><p className="text-muted-foreground mt-1 text-xs leading-relaxed">Open <a className="link" href={loginHost + "/_/auth/admin/code"} target="_blank" rel="noreferrer">{new URL(loginHost).host}/_/auth/admin/code</a> on any device, type the code and create your passkey there.</p></li>
            <li><p className="font-medium">3. Come back and sign in</p><p className="text-muted-foreground mt-1 text-xs leading-relaxed">You become the administrator with every tool. After that, this page only offers sign-in: setup never appears again.</p></li>
          </ol>
          <details className="mb-4 text-xs"><summary className="text-muted-foreground cursor-pointer">I have a setup link from the installer</summary>
            <form className="mt-2 flex items-center gap-2" onSubmit={submitPasted}>
              <Label htmlFor={`${id}-setup`} className="sr-only">Setup link</Label><Input id={`${id}-setup`} value={pasted} onChange={(event) => setPasted(event.target.value)} autoComplete="off" spellCheck={false} placeholder="http://ozal.localhost:…#enrol=…" />
              <Button type="submit" size="sm" disabled={!pasted.trim()}>Continue</Button>
            </form>
            {pasteError && <p className="text-destructive mt-2 text-xs" role="alert">{pasteError}</p>}
          </details>
        </> : <>
          <p className="text-muted-foreground mt-1 mb-4 text-sm">Nobody has an account on this console yet. The first administrator is created with a one-time setup link that only someone with access to the server can read, so nobody can get ahead of you.</p>
          <ol className="mb-5 grid gap-4 text-sm">
            <li><p className="font-medium">1. Get the setup link</p><p className="text-muted-foreground mt-1 text-xs leading-relaxed">The installer printed it when it finished. If it is gone or expired (it lasts 15 minutes), run this on the server for a new one:</p><code className="mt-2 block break-all text-xs">{enrolCommand}</code><div className="mt-2"><CopyText value={enrolCommand} label="Copy command" /></div></li>
            <li><p className="font-medium">2. Paste it here</p>
              <form className="mt-2 flex items-center gap-2" onSubmit={submitPasted}>
                <Label htmlFor={`${id}-setup`} className="sr-only">Setup link</Label><Input id={`${id}-setup`} value={pasted} onChange={(event) => setPasted(event.target.value)} autoComplete="off" spellCheck={false} placeholder="http://ozal.localhost:…#enrol=…" />
                <Button type="submit" size="sm" disabled={!pasted.trim()}>Continue</Button>
              </form>
              {pasteError && <p className="text-destructive mt-2 text-xs" role="alert">{pasteError}</p>}
            </li>
            <li><p className="font-medium">3. Create your passkey</p><p className="text-muted-foreground mt-1 text-xs leading-relaxed">You become the administrator with every tool. After that, this page only offers sign-in: setup never appears again.</p></li>
          </ol>
        </>}
        <p className="text-muted-foreground text-xs leading-relaxed">This computer cannot create a passkey? A host login lets you in for a while without creating the account.</p>
      </> : enrolled ? <>
        {notice && !error ? <p className="text-muted-foreground mt-1 text-sm" role="status">{notice}</p> : <p className="text-muted-foreground mt-1 text-sm">Administrators sign in with their passkey.</p>}
        {error && <p className="text-destructive mt-3 text-sm" role="alert">{error}</p>}
        <Button className="mt-6 w-full" onClick={() => void passkey()} disabled={waiting || !localHost}>{waiting ? "Waiting for your device…" : "Sign in with passkey"}</Button>
        {stuck && <div className="mt-3 flex flex-wrap gap-2">{(Object.keys(choices) as Choice[]).map((choice) => <Button key={choice} variant="outline" size="sm" onClick={() => void passkey(choices[choice].hints)}>{choices[choice].label}</Button>)}</div>}
        {loginHost && <p className="text-muted-foreground mt-3 text-xs leading-relaxed">You confirm it's you on <span className="text-foreground">{new URL(loginHost).host}</span>, this edge's sign-in address, where every kind of passkey works, and come back here. <button type="button" className="link" disabled={waiting || !localHost} onClick={() => void passkey(undefined, true)}>Use a passkey made for this console's own address</button></p>}
      </> : <p className="text-muted-foreground mt-2 text-sm" role="status">Checking…</p>}
      {!localHost && !enrol && mode === "passkey" && enrolled !== undefined && <p className="border-warning/40 bg-warning/[0.06] mt-4 rounded-md border px-3 py-2 text-xs" role="status">Open this console as <span className="font-mono">ozal.localhost:{location.port || "9091"}</span>: passkeys do not work on an IP address.</p>}
    </section>
    {!enrol && enrolled !== undefined && <div className="auth-links text-xs">{authLinks()}</div>}
  </div></main>;
}

function HostPanel() {
  const { notice } = useSession();
  const [picked, setPicked] = useState<string[]>(toolList.map(([id]) => id));
  const allTools = picked.length === toolList.length;
  const command = hostLoginCommand(location.origin, allTools ? undefined : picked);
  return <>
    <p className="text-muted-foreground mt-1 mb-4 text-sm">For whoever runs this server, when you cannot sign in with a passkey. Run one command on the server and open the link it prints. The link works once and lasts 5 minutes. It is not an account: the login is recorded under your server user name.</p>
    {notice && <p className="border-border bg-muted/40 mb-4 rounded-md border px-3 py-2 text-sm" role="status">{notice}</p>}
    <p className="text-muted-foreground mb-2 text-sm">On the server, run:</p>
    <code className="block break-all text-xs" data-testid="host-login-command">{command}</code>
    <div className="mt-2"><CopyText value={command} label="Copy command" /></div>
    <details className="mt-4 text-sm"><summary className="text-muted-foreground hover:text-foreground cursor-pointer">What this login may change ({allTools ? "everything" : `${picked.length} of ${toolList.length} tools`})</summary>
      <fieldset className="mt-3"><legend className="sr-only">What this login may change</legend><div className="grid grid-cols-2 gap-x-3 gap-y-2">{toolList.map(([id, label, description]) => <label key={id} title={description} className="flex items-center gap-2 text-sm"><input type="checkbox" checked={picked.includes(id)} onChange={(event) => setPicked((current) => event.target.checked ? [...current, id] : current.filter((item) => item !== id))} className="accent-[var(--primary)]" />{label}</label>)}</div></fieldset>
    </details>
    <p className="text-muted-foreground mt-3 text-xs">The login is recorded under your user name on the server. If <code className="font-mono">ozal</code> is not found, use the full path to the binary and add <code className="font-mono">-config /path/to/config.json</code>. Over SSH the command also prints how to forward the port.</p>
  </>;
}

// RequestPanel is how a person with no account asks to become an
// administrator. Asking grants nothing: an administrator reviews it, and the
// approval comes back to this browser only, which holds the ticket.
function RequestPanel() {
  const { setEnrol } = useSession();
  const [name, setName] = useState("");
  const [email, setEmail] = useState("");
  const [ticket, setTicket] = useState<AccessTicket | null>(null);
  const [phase, setPhase] = useState<"form" | "waiting" | "denied" | "gone">("form");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const id = useId();

  useEffect(() => {
    if (!ticket || phase !== "waiting") return;
    const controller = new AbortController();
    let timer = 0;
    const poll = async () => {
      try {
        const outcome = await checkAccessRequest(ticket, controller.signal);
        if (controller.signal.aborted) return;
        if (outcome.state === "approved") { setEnrol(outcome.token); return; }
        if (outcome.state === "denied" || outcome.state === "gone") { setPhase(outcome.state); return; }
      } catch { if (controller.signal.aborted) return; }
      timer = window.setTimeout(() => void poll(), pollEveryMs);
    };
    timer = window.setTimeout(() => void poll(), pollEveryMs);
    return () => { controller.abort(); window.clearTimeout(timer); };
  }, [ticket, phase, setEnrol]);

  const submit = async (event: FormEvent) => {
    event.preventDefault(); if (busy) return;
    setBusy(true); setError("");
    try { setTicket(await submitAccessRequest(name.trim(), email.trim())); setPhase("waiting"); }
    catch (cause) { setError((cause as Error).message); }
    finally { setBusy(false); }
  };

  if (phase === "waiting") return <div className="mt-2 grid gap-3" role="status" aria-live="polite">
    <p className="text-sm font-medium">Request sent. Waiting for an administrator.</p>
    <p className="text-muted-foreground text-xs leading-relaxed">Keep this page open. When an administrator approves, you will be asked to create your passkey right here. Nothing is granted until then, and the request expires after a day.</p>
  </div>;
  if (phase === "denied" || phase === "gone") return <div className="mt-2 grid gap-3" role="status">
    <p className="text-sm font-medium">{phase === "denied" ? "An administrator declined this request." : "This request expired or was already answered."}</p>
    <p className="text-muted-foreground text-xs leading-relaxed">{phase === "denied" ? "If you think it was a mistake, ask an administrator directly." : "If you still need access, send a new request."}</p>
    {phase === "gone" && <Button size="sm" variant="outline" onClick={() => { setTicket(null); setPhase("form"); }}>Send a new request</Button>}
  </div>;
  return <form className="mt-1 grid gap-4" onSubmit={(event) => void submit(event)}>
    <p className="text-muted-foreground text-sm">Tell an administrator who you are. They choose whether to let you in and which tools you get. You create your passkey only after they approve.</p>
    {error && <p className="text-destructive text-sm" role="alert">{error}</p>}
    <div className="grid gap-1.5"><Label htmlFor={`${id}-name`}>Full name</Label><Input id={`${id}-name`} value={name} onChange={(event) => setName(event.target.value)} required maxLength={80} autoComplete="name" /></div>
    <div className="grid gap-1.5"><Label htmlFor={`${id}-email`}>Email</Label><Input id={`${id}-email`} value={email} onChange={(event) => setEmail(event.target.value)} required type="email" maxLength={254} autoComplete="email" /><p className="text-muted-foreground text-xs">No message is sent. The administrator sees it as written, so use the one they know.</p></div>
    <Button type="submit" disabled={busy || !name.trim() || !email.trim()}>{busy ? "Sending…" : "Send request"}</Button>
  </form>;
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
  useEffect(() => { let live = true; void enrolInfo(token).then((value) => { if (!live) return; setInfo(value); setName((current) => current.trim() ? current : value.name ?? ""); setEmail((current) => current.trim() ? current : value.email ?? ""); }).catch((cause) => { if (live) setError((cause as Error).message); }); return () => { live = false; }; }, [token]);
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
    {!alternative && info?.kind !== "invite" && <details className="text-muted-foreground text-xs" open={stuck}><summary className="cursor-pointer">This computer cannot create a passkey?</summary><p className="mt-2 leading-relaxed">Close the browser's prompt, then run this on the server, . It lets you in for a while without creating the administrator account.</p><code>{hostLoginCommand(location.origin)}</code></details>}
  </form>;
}
