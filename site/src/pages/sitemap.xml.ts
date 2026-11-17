import type { APIRoute } from "astro";
import { ARTICLES } from "../lib/articles.mjs";
import { GUIDES } from "../lib/guides.mjs";

// Generated from the same list the pages use, so a new guide cannot be
// published without appearing here. The 404 page and the assets are not
// pages and stay out. `lastmod` is omitted on purpose: a build date is not a
// modification date, and an unreliable lastmod is worse than none.
const site = "https://ozal.dev";

export const GET: APIRoute = () => {
  const pages = [
    "/",
    ...GUIDES.map((guide) => (guide.slug ? `/docs/${guide.slug}/` : "/docs/")),
    ...ARTICLES.map((article) => article.path),
  ];
  const body = pages
    .map((page) => `  <url>\n    <loc>${new URL(page, site).toString()}</loc>\n  </url>`)
    .join("\n");
  return new Response(
    `<?xml version="1.0" encoding="UTF-8"?>\n<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">\n${body}\n</urlset>\n`,
    { headers: { "Content-Type": "application/xml; charset=utf-8" } },
  );
};
