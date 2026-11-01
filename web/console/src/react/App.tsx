import { lazy, Suspense } from "react";
import { Navigate, Route, Routes } from "react-router";

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
  return (
    <Routes>
      <Route path="/projects" element={<Navigate replace to="/sites" />} />
      <Route path="/simulator" element={<Navigate replace to="/activity" />} />
      <Route
        path="*"
        element={
          <Suspense fallback={<LoadingConsole />}>
            <Console />
          </Suspense>
        }
      />
    </Routes>
  );
}
