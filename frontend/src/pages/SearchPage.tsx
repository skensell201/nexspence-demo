import { useState, useMemo, useEffect, useRef, useCallback, FormEvent } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { Search, Package, ChevronDown, ChevronUp, ChevronsUpDown, ChevronRight, ExternalLink, HelpCircle } from 'lucide-react'
import { nexusApi } from '@/api/client'
import { Select } from '../components/Select'
import { HoloCard, HoloButton, HoloInput, HoloPill } from '@/components/holo'

interface SearchAsset {
  id: string
  path: string
  fileSize: number
  contentType: string
  lastModified: string
  lastDownloaded?: string | null
}

interface SearchComponent {
  id: string
  repository: string
  format: string
  group: string
  name: string
  version: string
  tags?: string[]
  lastDownloaded?: string | null
  assets?: SearchAsset[]
}

interface SearchResult { items: SearchComponent[] }

type SortKey = 'name' | 'version' | 'date'
type SortDir = 'asc' | 'desc'

const S = {
  page: { padding: 24, display: 'flex', flexDirection: 'column' as const, gap: 20 },
  header: { marginBottom: 4 },
  title: { fontSize: 20, fontWeight: 700, color: 'var(--holo-text)', margin: '0 0 4px' },
  subtitle: { fontSize: 13, color: 'var(--holo-text-dim)', margin: 0 },
  filterCard: {
    background: 'rgba(255,255,255,0.03)', border: '1px solid rgba(255,255,255,0.08)',
    borderRadius: 12, padding: '20px 20px 16px',
  },
  filterGrid: { display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(180px, 1fr))', gap: 12, marginBottom: 14 },
  field: { display: 'flex', flexDirection: 'column' as const, gap: 5 },
  label: { fontSize: 11, fontWeight: 600, color: 'var(--holo-text-dim)', textTransform: 'uppercase' as const, letterSpacing: '0.04em' },
  filterFooter: { display: 'flex', alignItems: 'center', gap: 10 },
  resultsLabel: { fontSize: 12, color: 'var(--holo-text-faint)', marginLeft: 'auto' as const },
  empty: { flex: 1, display: 'flex', flexDirection: 'column' as const, alignItems: 'center', justifyContent: 'center', gap: 12, color: 'var(--holo-text-faint)', fontSize: 14, paddingTop: 48 },

  group: { background: 'rgba(255,255,255,0.02)', border: '1px solid rgba(255,255,255,0.07)', borderRadius: 12, overflow: 'hidden' as const },
  groupHeader: {
    display: 'flex', alignItems: 'center', gap: 10,
    padding: '10px 16px',
    background: 'rgba(255,255,255,0.03)',
    borderBottom: '1px solid rgba(255,255,255,0.07)',
  },
  groupName: { fontSize: 13, fontWeight: 600, color: 'var(--holo-text)' },
  groupCount: { fontSize: 11, color: 'var(--holo-text-faint)', marginLeft: 'auto' as const },

  COLS: '1.5fr 1fr 1fr 2fr 1fr 1fr',
  thead: {
    display: 'grid',
    padding: '8px 16px',
    background: 'rgba(255,255,255,0.02)',
    borderBottom: '1px solid rgba(255,255,255,0.06)',
    fontSize: 11, fontWeight: 600, color: 'rgba(229,231,235,0.45)',
    textTransform: 'uppercase' as const, letterSpacing: '0.05em',
    userSelect: 'none' as const,
  },
  th: (active: boolean) => ({
    display: 'flex', alignItems: 'center', gap: 4, cursor: 'pointer',
    color: active ? '#93c5fd' : 'rgba(229,231,235,0.45)',
  }),
  trow: {
    display: 'grid',
    padding: '10px 16px', borderBottom: '1px solid rgba(255,255,255,0.04)',
    fontSize: 13, color: 'var(--holo-text)', alignItems: 'center',
    cursor: 'pointer',
  },
  expanded: {
    padding: '8px 16px 12px 32px',
    borderBottom: '1px solid rgba(255,255,255,0.04)',
    background: 'rgba(0,0,0,0.15)',
  },
  assetRow: {
    display: 'flex', justifyContent: 'space-between', alignItems: 'center',
    padding: '4px 0', fontSize: 12, gap: 12,
  },
  muted: { fontSize: 12, color: 'var(--holo-text-faint)' },
  path: { fontSize: 11, color: 'rgba(147,197,253,0.85)', fontFamily: 'monospace' as const, wordBreak: 'break-all' as const },
}

const FORMAT_COLORS: Record<string, string> = {
  maven2: '#f97316', npm: '#ef4444', docker: '#3b82f6', oci: '#5b8def', pypi: '#a78bfa',
  go: '#06b6d4', nuget: '#8b5cf6', helm: '#0ea5e9', raw: '#6b7280', apt: '#f59e0b', yum: '#10b981',
  cran: '#276dc3', alpine: '#0d597f',
}

function fmtSize(b: number | undefined) {
  if (!b) return '—'
  if (b < 1024) return b + ' B'
  if (b < 1024 * 1024) return (b / 1024).toFixed(1) + ' KB'
  return (b / 1024 / 1024).toFixed(1) + ' MB'
}

function fmtDate(s: string | undefined) {
  if (!s) return '—'
  const d = new Date(s)
  return isNaN(d.getTime()) ? '—' : d.toLocaleDateString()
}

function SortIcon({ col, sortKey, sortDir }: { col: SortKey; sortKey: SortKey; sortDir: SortDir }) {
  if (col !== sortKey) return <ChevronsUpDown size={11} style={{ opacity: 0.4 }} />
  return sortDir === 'asc' ? <ChevronUp size={11} /> : <ChevronDown size={11} />
}

const EMPTY_FILTERS = { repository: '', format: '', name: '', group: '', version: '', tag: '' }
type Filters = typeof EMPTY_FILTERS

// URL param keys kept short and stable so Browse-back restores state.
const URL_KEYS: Record<keyof Filters, string> = {
  name: 'q', format: 'format', repository: 'repo', version: 'version', group: 'group', tag: 'tag',
}

function filtersFromURL(sp: URLSearchParams): Filters {
  return {
    name:       sp.get(URL_KEYS.name)       ?? '',
    format:     sp.get(URL_KEYS.format)     ?? '',
    repository: sp.get(URL_KEYS.repository) ?? '',
    version:    sp.get(URL_KEYS.version)    ?? '',
    group:      sp.get(URL_KEYS.group)      ?? '',
    tag:        sp.get(URL_KEYS.tag)        ?? '',
  }
}

function filtersToURL(f: Filters): Record<string, string> {
  const out: Record<string, string> = {}
  for (const k of Object.keys(URL_KEYS) as (keyof Filters)[]) {
    if (f[k]) out[URL_KEYS[k]] = f[k]
  }
  return out
}

// sessionStorage key used to restore scroll/highlight when Back-navigating from Browse.
const RETURN_KEY = 'search:lastClickedComponentId'

const PAGE_SIZE = 50

export default function SearchPage() {
  const navigate = useNavigate()
  const [searchParams, setSearchParams] = useSearchParams()
  const [filters, setFilters] = useState<Filters>(() => filtersFromURL(searchParams))
  const [sortKey, setSortKey] = useState<SortKey>('name')
  const [sortDir, setSortDir] = useState<SortDir>('asc')
  const [expanded, setExpanded] = useState<Set<string>>(new Set())
  const [hoveredCId, setHoveredCId] = useState<string | null>(null)
  const [returnHighlight, setReturnHighlight] = useState<string | null>(null)
  const [visibleCount, setVisibleCount] = useState(PAGE_SIZE)
  const returnRowRef = useRef<HTMLDivElement | null>(null)

  // Submitted state is derived from the URL so browser Back (from Browse) restores it.
  const submitted = useMemo<Filters | null>(() => {
    const f = filtersFromURL(searchParams)
    return Object.values(f).some(v => v !== '') ? f : null
  }, [searchParams])

  const { data, isLoading } = useQuery<SearchResult>({
    queryKey: ['search', submitted],
    queryFn: () => {
      const p: Record<string, string> = {}
      if (submitted!.repository) p.repository = submitted!.repository
      if (submitted!.format)     p.format     = submitted!.format
      if (submitted!.name)       p.name       = submitted!.name
      if (submitted!.group)      p.group      = submitted!.group
      if (submitted!.version)    p.version    = submitted!.version
      if (submitted!.tag)        p.tag        = submitted!.tag
      return nexusApi.search(p).then(r => r.data)
    },
    enabled: !!submitted,
  })

  // Back-from-Browse: if sessionStorage holds a component ID and it's in the current results,
  // highlight and scroll it into view. Clear the key so it fires only once per navigation.
  useEffect(() => {
    if (!data?.items?.length) return
    let cid: string | null = null
    try { cid = sessionStorage.getItem(RETURN_KEY) } catch { /* ignore */ }
    if (!cid) return
    const hit = data.items.find(c => c.id === cid)
    if (!hit) { try { sessionStorage.removeItem(RETURN_KEY) } catch { /* sessionStorage may be unavailable */ }; return }
    setReturnHighlight(cid)
    try { sessionStorage.removeItem(RETURN_KEY) } catch { /* sessionStorage may be unavailable */ }
    // Fade out highlight after a couple of seconds.
    const t = setTimeout(() => setReturnHighlight(null), 2500)
    return () => clearTimeout(t)
  }, [data])

  useEffect(() => {
    if (!returnHighlight) return
    returnRowRef.current?.scrollIntoView({ behavior: 'smooth', block: 'center' })
  }, [returnHighlight])

  const allItems = useMemo(() => data?.items ?? [], [data])

  // OCI Distribution digest-alias components (version = "sha256:...") are filtered from the
  // main list but kept in a lookup map so they can appear inside the expanded view of their
  // parent tag. Both 'docker' and 'oci' formats speak the OCI Distribution protocol and get
  // the same digest-alias component created on every manifest push.
  const dockerDigests = useMemo(() => {
    const map = new Map<string, SearchComponent[]>()
    for (const c of allItems) {
      if ((c.format === 'docker' || c.format === 'oci') && c.version?.startsWith('sha256:')) {
        const key = `${c.repository}::${c.name}`
        const arr = map.get(key) ?? []
        arr.push(c)
        map.set(key, arr)
      }
    }
    return map
  }, [allItems])

  const items = useMemo(() =>
    allItems.filter(c => !((c.format === 'docker' || c.format === 'oci') && c.version?.startsWith('sha256:')))
  , [allItems])

  const sorted = useMemo(() => {
    return [...items].sort((a, b) => {
      let cmp = 0
      if (sortKey === 'name')    cmp = (a.name ?? '').localeCompare(b.name ?? '')
      if (sortKey === 'version') cmp = (a.version ?? '').localeCompare(b.version ?? '')
      if (sortKey === 'date')    cmp = (a.assets?.[0]?.lastModified ?? '').localeCompare(b.assets?.[0]?.lastModified ?? '')
      return sortDir === 'asc' ? cmp : -cmp
    })
  }, [items, sortKey, sortDir])

  // Reset visible count whenever the sorted result set changes (new query or sort change).
  useEffect(() => { setVisibleCount(PAGE_SIZE) }, [sorted])

  const visibleItems = useMemo(() => sorted.slice(0, visibleCount), [sorted, visibleCount])

  const grouped = useMemo(() => {
    const map = new Map<string, SearchComponent[]>()
    for (const c of visibleItems) {
      const arr = map.get(c.repository) ?? []
      arr.push(c)
      map.set(c.repository, arr)
    }
    return map
  }, [visibleItems])

  const repoCount = useMemo(
    () => new Set(items.map((c) => c.repository)).size,
    [items],
  )

  const showMore = useCallback(() => setVisibleCount(v => v + PAGE_SIZE), [])

  function handleSort(key: SortKey) {
    if (sortKey === key) setSortDir(d => d === 'asc' ? 'desc' : 'asc')
    else { setSortKey(key); setSortDir('asc') }
  }

  function toggleExpand(id: string) {
    setExpanded(prev => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  const handleSubmit = (e: FormEvent) => { e.preventDefault(); setSearchParams(filtersToURL(filters)) }
  const handleClear  = () => { setFilters(EMPTY_FILTERS); setSearchParams({}); setExpanded(new Set()) }
  const set = (key: keyof typeof EMPTY_FILTERS) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) =>
    setFilters(f => ({ ...f, [key]: e.target.value }))

  const theadStyle = { ...S.thead, gridTemplateColumns: S.COLS }
  const trowStyle  = { ...S.trow,  gridTemplateColumns: S.COLS }

  return (
    <div style={S.page}>
      <div style={{ marginBottom: 24 }}>
        <div className="holo-section-label" style={{ marginBottom: 4 }}>WORKSPACE / SEARCH</div>
        <h1 style={{ fontSize: 20, fontWeight: 700, margin: '0 0 3px', letterSpacing: '-0.01em', lineHeight: 1.2, background: 'linear-gradient(110deg, #7c5cff, #22d3ee 60%)', WebkitBackgroundClip: 'text', WebkitTextFillColor: 'transparent', backgroundClip: 'text' as const }}>Search</h1>
        <p style={{ fontSize: 12, color: 'var(--holo-text-faint)', margin: 0 }}>Find artifacts across all repositories</p>
      </div>

      <form style={S.filterCard} onSubmit={handleSubmit}>
        <div style={S.filterGrid}>
          <div style={S.field}>
            <label style={S.label}>Name</label>
            <HoloInput placeholder="e.g. spring-core" value={filters.name} onChange={set('name')} />
          </div>
          <div style={S.field}>
            <label style={S.label}>Format</label>
            <Select
              options={[
                { value: '', label: 'any' },
                ...['maven2','npm','docker','oci','pypi','go','nuget','helm','raw','apt','yum','cran','alpine'].map(f => ({ value: f, label: f })),
              ]}
              value={filters.format}
              onChange={v => setFilters(f => ({ ...f, format: v }))}
            />
          </div>
          <div style={S.field}>
            <label style={S.label}>Repository</label>
            <HoloInput placeholder="any" value={filters.repository} onChange={set('repository')} />
          </div>
          <div style={S.field}>
            <label style={S.label}>Version</label>
            <HoloInput placeholder="e.g. 1.2.3" value={filters.version} onChange={set('version')} />
          </div>
          <div style={S.field}>
            <label style={{ ...S.label, display: 'flex', alignItems: 'center', gap: 4 }}>
              Group
              <span
                title="Maven groupId / npm scope. Leave blank to search across all groups."
                style={{ display: 'inline-flex', cursor: 'help', opacity: 0.55 }}
                aria-label="Group hint"
              >
                <HelpCircle size={11} />
              </span>
            </label>
            <HoloInput
              placeholder="e.g. org.springframework"
              title="Maven groupId / npm scope. Leave blank to search across all groups."
              value={filters.group}
              onChange={set('group')}
            />
          </div>
          <div style={{ ...S.field, gridColumn: '1 / -1' }}>
            <label style={{ ...S.label, color: '#7c5cff' }}>Tag</label>
            <HoloInput
              placeholder="e.g. prod  or  team:backend"
              value={filters.tag}
              onChange={set('tag')}
              style={{ borderColor: filters.tag ? 'rgba(124,92,255,0.5)' : undefined }}
            />
          </div>
        </div>
        <div style={S.filterFooter}>
          <HoloButton type="submit" variant="primary" icon={<Search size={14} />}>Search</HoloButton>
          <HoloButton type="button" onClick={handleClear}>Clear</HoloButton>
          {submitted && !isLoading && (
            <span style={S.resultsLabel}>{items.length} result{items.length !== 1 ? 's' : ''} in {repoCount} repo{repoCount !== 1 ? 's' : ''}</span>
          )}
        </div>
      </form>

      {!submitted ? (
        <div style={S.empty}>
          <Search size={40} style={{ opacity: 0.3 }} />
          <p>Enter filters and click Search</p>
        </div>
      ) : isLoading ? (
        <div style={S.empty}>Searching…</div>
      ) : items.length === 0 ? (
        <div style={S.empty}>
          <Package size={40} style={{ opacity: 0.3 }} />
          <p>No results matched your filters</p>
        </div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
          {/* Sortable column headers — shown once above all groups */}
          <div style={theadStyle}>
            <div style={S.th(sortKey === 'name')} onClick={() => handleSort('name')}>
              Name <SortIcon col="name" sortKey={sortKey} sortDir={sortDir} />
            </div>
            <div>Group</div>
            <div style={S.th(sortKey === 'version')} onClick={() => handleSort('version')}>
              Version <SortIcon col="version" sortKey={sortKey} sortDir={sortDir} />
            </div>
            <div>Path</div>
            <div style={S.th(sortKey === 'date')} onClick={() => handleSort('date')}>
              Modified <SortIcon col="date" sortKey={sortKey} sortDir={sortDir} />
            </div>
            <div>Tags</div>
          </div>

          {[...grouped.entries()].map(([repo, comps]: [string, SearchComponent[]]) => {
            const fmt = comps[0].format
            const color = FORMAT_COLORS[fmt] ?? '#6b7280'
            return (
              <div key={repo} style={S.group}>
                <div style={S.groupHeader}>
                  <HoloPill style={{ background: color + '22', color }}>{fmt}</HoloPill>
                  <span style={S.groupName}>{repo}</span>
                  <span style={S.groupCount}>{comps.length} component{comps.length !== 1 ? 's' : ''}</span>
                  <HoloButton
                    onClick={() => navigate(`/browse?repo=${encodeURIComponent(repo)}`)}
                    style={{ marginLeft: 8 }}
                    title="Open in Browse"
                    icon={<ExternalLink size={12} />}
                  >Browse</HoloButton>
                </div>
                {comps.map(c => {
                  const firstAsset = c.assets?.[0]
                  const isOpen = expanded.has(c.id)
                  const hasMulti = (c.assets?.length ?? 0) > 1
                  const isReturnRow = returnHighlight === c.id
                  const openInBrowse = () => {
                    // Remember where we were so Back-navigation can scroll here.
                    try { sessionStorage.setItem(RETURN_KEY, c.id) } catch { /* ignore */ }
                    const qp = new URLSearchParams({ repo: c.repository, cid: c.id })
                    if (firstAsset?.path) qp.set('asset', firstAsset.path)
                    navigate(`/browse?${qp.toString()}`)
                  }
                  return (
                    <div key={c.id} ref={isReturnRow ? returnRowRef : undefined} data-testid="search-result-row">
                      <HoloCard
                        style={{
                          padding: 14,
                          marginBottom: 8,
                          transition: 'border-color 0.15s, background 0.15s',
                          ...(isReturnRow
                            ? { outline: '1px solid rgba(59,130,246,0.6)', background: 'rgba(59,130,246,0.08)', transition: 'background 0.6s, outline 0.6s' }
                            : hoveredCId === c.id
                              ? { borderColor: 'rgba(124,92,255,0.4)', background: 'rgba(124,92,255,0.04)' }
                              : {}),
                        }}
                        onMouseEnter={() => setHoveredCId(c.id)}
                        onMouseLeave={() => setHoveredCId(null)}
                      >
                        <div
                          style={trowStyle}
                          title="Open in Browse"
                          onClick={openInBrowse}
                        >
                          <div style={{ fontWeight: 500, color: 'var(--holo-text)', display: 'flex', alignItems: 'center', gap: 6 }}>
                            <span
                              onClick={e => { e.stopPropagation(); toggleExpand(c.id) }}
                              title={isOpen ? 'Collapse' : 'Expand assets'}
                              style={{ display: 'inline-flex', alignItems: 'center', cursor: 'pointer', padding: 2, margin: -2, borderRadius: 3 }}
                            >
                              <ChevronRight size={12} style={{ color: 'var(--holo-text-dim)', transform: isOpen ? 'rotate(90deg)' : undefined, transition: 'transform 0.15s', flexShrink: 0 }} />
                            </span>
                            {c.name || '—'}
                          </div>
                          <div style={S.muted}>{c.group || '—'}</div>
                          <div style={{ color: '#a5b4fc', fontFamily: 'monospace', fontSize: 12 }}>{c.version || '—'}</div>
                          <div style={S.path}>{firstAsset?.path ?? '—'}{hasMulti ? ` +${c.assets!.length - 1}` : ''}</div>
                          <div style={{ display: 'flex', flexDirection: 'column', gap: 1 }}>
                            <span style={S.muted}>{fmtDate(firstAsset?.lastModified)} {firstAsset ? `· ${fmtSize(firstAsset.fileSize)}` : ''}</span>
                            {(firstAsset?.lastDownloaded ?? c.lastDownloaded) && (
                              <span style={{ fontSize: 10, color: 'var(--holo-text-faint)' }}>↓ {fmtDate(firstAsset?.lastDownloaded ?? c.lastDownloaded ?? undefined)}</span>
                            )}
                          </div>
                          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 3 }}>
                            {(c.tags ?? []).map(t => (
                              <span key={t} style={{
                                background: 'rgba(124,92,255,0.12)',
                                border: '1px solid rgba(124,92,255,0.25)',
                                borderRadius: 4, padding: '2px 6px',
                                fontSize: 10, color: '#a78bfa', fontFamily: 'monospace',
                              }}>{t}</span>
                            ))}
                          </div>
                        </div>
                        {isOpen && (
                          <div style={S.expanded}>
                            {(c.assets ?? []).map(a => (
                              <div key={a.id} style={S.assetRow}>
                                <span style={S.path}>{a.path}</span>
                                <span style={{ ...S.muted, whiteSpace: 'nowrap' as const }}>
                                  {fmtSize(a.fileSize)} · {a.contentType || '—'} · {fmtDate(a.lastModified)}
                                  {a.lastDownloaded && <span style={{ color: 'var(--holo-text-faint)', marginLeft: 4 }}>↓ {fmtDate(a.lastDownloaded)}</span>}
                                </span>
                              </div>
                            ))}
                            {(() => {
                              const digests = dockerDigests.get(`${c.repository}::${c.name}`) ?? []
                              if (digests.length === 0) return null
                              return (
                                <>
                                  <div style={{ ...S.muted, marginTop: 8, marginBottom: 4, fontSize: 11, textTransform: 'uppercase' as const, letterSpacing: '0.05em' }}>Digest aliases</div>
                                  {digests.flatMap(d => (d.assets ?? []).map(a => (
                                    <div key={a.id} style={S.assetRow}>
                                      <span style={{ ...S.path, color: 'rgba(147,197,253,0.5)' }}>{a.path}</span>
                                      <span style={{ ...S.muted, whiteSpace: 'nowrap' as const }}>{d.version?.slice(0, 19)}… · {fmtSize(a.fileSize)}</span>
                                    </div>
                                  )))}
                                </>
                              )
                            })()}
                          </div>
                        )}
                      </HoloCard>
                    </div>
                  )
                })}
              </div>
            )
          })}
          {visibleCount < sorted.length && (
            <div style={{ display: 'flex', justifyContent: 'center', paddingTop: 8 }}>
              <HoloButton onClick={showMore}>
                Show more ({sorted.length - visibleCount} remaining)
              </HoloButton>
            </div>
          )}
        </div>
      )}
    </div>
  )
}
