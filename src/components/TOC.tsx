import { useEffect, useState } from 'react'

interface TOCItem {
  id: string
  text: string
  level: number
}

export function TOC() {
  const [headings, setHeadings] = useState<TOCItem[]>([])

  useEffect(() => {
    const article = document.querySelector('article')
    if (!article) return

    const headingElements = article.querySelectorAll('h1, h2, h3, h4, h5, h6')
    const tocItems: TOCItem[] = Array.from(headingElements).map((el) => {
      const level = parseInt(el.tagName.charAt(1))
      const text = el.textContent || ''
      const id = el.id || text.toLowerCase().replace(/\s+/g, '-').replace(/[^\w-]/g, '')
      el.id = id // ensure id is set
      return { id, text, level }
    })

    setHeadings(tocItems)
  }, [])

  if (headings.length === 0) return null

  return (
    <div className="mb-6">
      <h2 className="text-lg font-semibold mb-2">Daftar Isi</h2>
      <nav>
        <ul className="space-y-1">
          {headings.map((item) => (
            <li key={item.id} style={{ paddingLeft: `${(item.level - 1) * 1}rem` }}>
              <a
                href={`#${item.id}`}
                className="text-sm text-muted-foreground hover:text-foreground transition-colors"
              >
                {item.text}
              </a>
            </li>
          ))}
        </ul>
      </nav>
    </div>
  )
}