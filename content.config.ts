import { defineCollection, z } from 'astro:content';

// Define 'docs' collection for versioned documentation
const docsCollection = defineCollection({
  schema: z.object({
    title: z.string(),
  }),
});

export const collections = {
  docs: docsCollection,
};
