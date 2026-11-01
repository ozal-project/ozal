import { lazy, Suspense, useEffect } from "react";
import { Navigate, Route, Routes, useLocation, useNavigate } from "react-router";
import { returningFromGitHub, returnTarget } from "$lib/githubReturn";
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
  const { session } = useSession();
  const location = useLocation();
  const navigate = useNavigate();
  useEffect(() => {
    if (location.pathname === "/applications" && returningFromGitHub(location.search)) {
      navigate(`${returnTarget()}${location.search}`, { replace: true });
    }
  }, [location.pathname, location.search, navigate]);
  useEffect(() => { document.title = session ? "Overview · Ozal" : "Sign in · Ozal"; }, [session]);
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
