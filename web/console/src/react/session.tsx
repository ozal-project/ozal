import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { configureAPISession } from "$lib/apiSession";
import { exchangeHostLogin, resumeSession, takeHostLink, type ResumedSession } from "$lib/operatorLogin";
import { finishLoginHost } from "$lib/loginHost";
import { renewSession } from "$lib/passkey";

export type OperatorSession = ResumedSession & { passkey: boolean };
type SessionContextValue = {
  session: OperatorSession | null;
  ready: boolean;
  notice: string;
  enrol: string;
  start: (value: Partial<OperatorSession> & Pick<OperatorSession, "token">, passkey: boolean) => void;
  end: (message?: string) => void;
  setEnrol: (value: string) => void;
};
const SessionContext = createContext<SessionContextValue | null>(null);

export function SessionProvider({ children }: { children: ReactNode }) {
  const [session, setSession] = useState<OperatorSession | null>(null);
  const [ready, setReady] = useState(false);
  const [notice, setNotice] = useState("");
  const [enrol, setEnrol] = useState("");
  const sessionRef = useRef(session);
  sessionRef.current = session;
  const start = useCallback((value: Partial<OperatorSession> & Pick<OperatorSession, "token">, passkey: boolean) => {
    setNotice(""); setEnrol("");
    setSession({ token: value.token, name: value.name ?? "", expires: value.expires ?? "", tools: value.tools ?? [], renewable: !!value.renewable, passkey });
  }, []);
  const end = useCallback((message = "") => { setSession(null); setNotice(message); }, []);
  const updateEnrol = useCallback((value: string) => setEnrol(value), []);

  useEffect(() => {
    configureAPISession({ token: () => sessionRef.current?.token ?? "", unauthorized: () => end("This session is no longer valid. Sign in again.") });
  }, []);

  useEffect(() => {
    let live = true;
    const consume = async () => {
      const link = takeHostLink();
      try {
        if (link?.kind === "enrol") { setEnrol(link.token); setReady(true); return; }
        if (link?.kind === "login_host") start(await finishLoginHost(link.token), true);
        else if (link) start(await exchangeHostLogin(link.token), false);
        else {
          const resumed = await resumeSession();
          if (resumed && !sessionRef.current) start(resumed, resumed.passkey);
        }
      } catch (error) { setNotice((error as Error).message); }
      finally { if (live) setReady(true); }
    };
    void consume();
    const onHash = () => { setReady(false); void consume(); };
    addEventListener("hashchange", onHash);
    return () => { live = false; removeEventListener("hashchange", onHash); };
  }, []);

  const value = useMemo(() => ({ session, ready, notice, enrol, start, end, setEnrol: updateEnrol }), [session, ready, notice, enrol, start, end, updateEnrol]);
  return <SessionContext.Provider value={value}>{children}</SessionContext.Provider>;
}

export function useSession() {
  const value = useContext(SessionContext);
  if (!value) throw new Error("useSession must be used inside SessionProvider");
  return value;
}

export function SessionLifetime() {
  const { session, end, start } = useSession();
  const lastActive = useRef(Date.now());
  const [warning, setWarning] = useState(false);
  useEffect(() => {
    const active = () => { lastActive.current = Date.now(); setWarning(false); };
    const events = ["keydown", "pointerdown", "wheel", "touchstart"] as const;
    events.forEach((event) => addEventListener(event, active, { passive: true }));
    let renewing = false;
    const timer = setInterval(() => {
      if (!session?.token || !session.expires) { setWarning(false); return; }
      const left = Date.parse(session.expires) - Date.now();
      if (left <= 0) { end("Your session ended. Sign in again."); return; }
      const recent = Date.now() - lastActive.current < 5 * 60_000;
      if (session.renewable && recent && left < 10 * 60_000 && !renewing) {
        renewing = true;
        void renewSession(session.token).then((answer) => {
          if (answer.expires) start({ ...session, expires: answer.expires }, session.passkey);
        }).catch(() => {}).finally(() => { renewing = false; });
      }
      setWarning(left < 2 * 60_000 && !(session.renewable && recent));
    }, 5_000);
    return () => { clearInterval(timer); events.forEach((event) => removeEventListener(event, active)); };
  }, [session, end, start]);
  return warning ? <div className="bg-popover border-warning/40 fixed right-4 bottom-4 z-50 flex max-w-sm items-center gap-3 rounded-xl border px-4 py-3 text-sm shadow-2xl" role="status"><span className="lamp lamp-warn" aria-hidden="true" /><span>{session?.renewable ? "Your session ends in under two minutes." : "This host login ends in under two minutes; ask for a new one to continue."}</span></div> : null;
}
