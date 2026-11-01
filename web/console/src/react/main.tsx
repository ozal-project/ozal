import { createRoot } from "react-dom/client";
import "./app.css";
import { Root } from "./Root";
import { SessionProvider } from "./session";

const target = document.getElementById("app");
if (!target) throw new Error("React preview mount is missing");

createRoot(target).render(<SessionProvider><Root /></SessionProvider>);
