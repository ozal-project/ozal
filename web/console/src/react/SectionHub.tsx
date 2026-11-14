import { ArrowRight } from "lucide-react";
import { Link } from "react-router";
import { target, visibleGroups } from "$lib/navigation";
import { useSession } from "./session";

// The Overview ends with every place of the console, grouped and explained,
// the way the header's switcher lists them: a map for a first visit, and a
// way in that never needs the menu.
export function SectionHub() {
  const { session } = useSession();
  const tools = session?.tools ?? [];
  const groups = visibleGroups(tools).filter((group) => group.name);
  return <section aria-labelledby="overview-sections" className="mt-4">
    <h2 id="overview-sections" className="plate">Everything in this console</h2>
    <div className="section-hub mt-3">
      {groups.map((group) => <div key={group.name} className="section-hub-group">
        <p className="section-hub-heading">{group.name}</p>
        <ul>{group.sections.map((section) => <li key={section.path}>
          <Link to={target(section, tools)} className="section-hub-item">
            <span className="min-w-0"><span className="section-hub-label">{section.label}</span><span className="section-hub-purpose">{section.purpose}</span></span>
            <ArrowRight className="section-hub-arrow size-4" aria-hidden="true" />
          </Link>
        </li>)}</ul>
      </div>)}
    </div>
  </section>;
}
