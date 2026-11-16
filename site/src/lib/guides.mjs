// The public guides that ozal.dev/docs renders from the repository's own
// Markdown. The files stay the single source: a guide changes in the same
// commit as the code it describes, and the site shows it on the next build.
// Contributor documents (design, engineering, stack) stay on GitHub only.
// Paths are relative to the repository root; slug "" is /docs/ itself.

export const REPO = "https://github.com/ozal-project/ozal";

/** @type {{ path: string, slug: string, nav: string }[]} */
export const GUIDES = [
  { path: "docs/START-HERE.md", slug: "", nav: "Start here" },
  { path: "QUICKSTART.md", slug: "quickstart", nav: "Quickstart" },
  { path: "docs/operations/download-install.md", slug: "install", nav: "Install" },
  { path: "docs/operations/first-application.md", slug: "first-application", nav: "Publish an application" },
  { path: "docs/operations/administration.md", slug: "administration", nav: "Administer" },
  { path: "docs/security.md", slug: "security", nav: "Security" },
  { path: "docs/reference.md", slug: "reference", nav: "Configuration and CLI" },
];

/** The site URL of a guide, or undefined when the path is not a public guide.
 * @param {string} path repository-relative path of a Markdown file */
export const guideHref = (path) => {
  const guide = GUIDES.find((g) => g.path === path);
  return guide && (guide.slug ? `/docs/${guide.slug}/` : "/docs/");
};

/** Rewrites the links of a rendered guide for the site: a link to another
 * public guide becomes its /docs/ page (anchor kept), a link to any other
 * repository file becomes its GitHub page. External links and in-page
 * anchors are left alone. The HTML is the Markdown renderer's own output,
 * whose attributes are always double-quoted.
 * @param {string} html @param {string} from repository-relative path of the guide */
export const rewriteGuideLinks = (html, from) =>
  html.replace(/(<a\b[^>]*?\shref=")([^"]*)"/g, (all, open, href) => {
    if (href === "" || href.startsWith("#") || /^[a-z][a-z0-9+.-]*:/i.test(href)) return all;
    const [path, hash] = href.split("#");
    const target = normalize(`${dirname(from)}/${path}`);
    if (target.startsWith("../")) throw new Error(`${from}: link leaves the repository: ${href}`);
    const anchor = hash ? `#${hash}` : "";
    return `${open}${guideHref(target) ?? `${REPO}/blob/main/${target}`}${anchor}"`;
  });

/** @param {string} path */
const dirname = (path) => (path.includes("/") ? path.slice(0, path.lastIndexOf("/")) : ".");

/** Resolves "." and ".." segments of a relative path.
 * @param {string} path */
const normalize = (path) => {
  /** @type {string[]} */
  const out = [];
  for (const part of path.split("/")) {
    if (part === "" || part === ".") continue;
    if (part === ".." && out.length && out[out.length - 1] !== "..") out.pop();
    else out.push(part);
  }
  return out.join("/");
};
