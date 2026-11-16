// The public guides, loaded from the repository's own Markdown (see
// src/lib/guides.mjs). Nothing is copied into site/.
import { defineCollection } from "astro:content";
import { glob } from "astro/loaders";
import { GUIDES } from "./lib/guides.mjs";

const guides = defineCollection({
  loader: glob({
    base: "..",
    pattern: GUIDES.map((g) => g.path),
    // The page route is the guide's slug; the index guide is "index".
    generateId: ({ entry }) => GUIDES.find((g) => g.path === entry)?.slug || "index",
  }),
});

export const collections = { guides };
