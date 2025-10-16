// Helper to discover docs markdown pages using Vite's import.meta.glob
export type DocPage = {
  version: string
  category: string
  slug: string
  title?: string
  order?: number
  module: any
}

export function getAllDocs() {
  // import all index.md files under src/content/docs/**/index.md
  const modules = import.meta.glob('../content/docs/**/index.md', { eager: true }) as Record<string, any>

  const pages: DocPage[] = []

  for (const path in modules) {
    // path looks like ../content/docs/v1.0/getting-started/index.md
    const parts = path.split('/')
    // find 'docs' index
    const docsIndex = parts.findIndex((p) => p === 'docs')
    if (docsIndex === -1) continue
  const version = parts[docsIndex + 1]
  // Strip '.md' extension for version-level index files
  const rawCategory = parts[docsIndex + 2]
  const category = rawCategory.replace(/\.md$/, '')
    const mod = modules[path]
      const title = (mod && mod.frontmatter && mod.frontmatter.title) || (category || '')
      const order = (mod && mod.frontmatter && typeof mod.frontmatter.order === 'number') ? mod.frontmatter.order : 999
      pages.push({ version, category, slug: `${version}/${category}`, title, order, module: mod })
  }

  return pages
}

export function findDoc(version: string, category: string) {
  const pages = getAllDocs()
  return pages.find((p) => p.version === version && p.category === category)
}
