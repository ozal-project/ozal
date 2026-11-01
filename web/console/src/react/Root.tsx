import { lazy, Suspense } from "react";
import { AuthScreen } from "./AuthScreen";
import { useSession } from "./session";

const ConsoleRouter = lazy(() => import("./ConsoleRouter"));

export function Root() {
  const { session, ready } = useSession();
  if (!ready) {
    return <div className="text-muted-foreground flex min-h-screen items-center justify-center text-sm" role="status">Checking your session…</div>;
  }
  if (!session) return <AuthScreen />;
  return <Suspense fallback={<div className="text-muted-foreground flex min-h-screen items-center justify-center text-sm" role="status">Opening the console…</div>}><ConsoleRouter /></Suspense>;
}
