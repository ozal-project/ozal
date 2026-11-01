import { createRoot } from "react-dom/client";
import { BrowserRouter } from "react-router";
import "./app.css";
import { App } from "./App";
import { SessionProvider } from "./session";

const target = document.getElementById("app");
if (!target) throw new Error("React preview mount is missing");

createRoot(target).render(<SessionProvider><BrowserRouter><App /></BrowserRouter></SessionProvider>);
