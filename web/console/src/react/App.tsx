import { lazy, Suspense, useEffect } from "react";
import { Navigate, Route, Routes } from "react-router";
import { AuthScreen } from "./AuthScreen";
import { SessionLifetime, useSession } from "./session";

const Console = lazy(() => import("./Console"));

function LoadingConsole() {
  return (
    <div
      className="flex min-h-screen items-center justify-center text-sm text-muted-foreground"
      role="status"
    >
      Opening the console…
    </div>
  );
}

export function App() {
  const { session, ready } = useSession();
  useEffect(() => { document.title = session ? "Overview · Ozal" : "Sign in · Ozal"; }, [session]);
  if (!ready) return <div className="text-muted-foreground flex min-h-screen items-center justify-center text-sm" role="status">Checking your session…</div>;
  if (!session) return <AuthScreen />;
  return (
    <>
      <Routes>
        <Route path="/projects" element={<Navigate replace to="/sites" />} />
        <Route path="/simulator" element={<Navigate replace to="/activity" />} />
        <Route path="*" element={<Suspense fallback={<LoadingConsole />}><Console /></Suspense>} />
      </Routes>
      <SessionLifetime />
    </>
  );
}
