import { useState } from 'react'
import * as React from 'react'
import { useNavigate } from 'react-router-dom'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Database, Download, Plus, Trash2, RefreshCw, Settings2, Power } from 'lucide-react'
import { nexusApi, nexspenceApi, apiClient, apiErrorMessage, BlobStoreMigration, startBlobStoreMigration, getBlobStoreMigration, cancelBlobStoreMigration, RoutingRule } from '@/api/client'
import { useAuthStore } from '@/store/authStore'
import styles from './RepositoriesPage.module.css'
import { Select } from '../components/Select'
import { HoloButton, HoloInput, HoloPill, HoloModal, Wizard } from '@/components/holo'
import { Truncated } from '@/components/Truncated'

interface Repository {
  id: string
  name: string
  format: string
  type: string
  online: boolean
  allowAnonymous: boolean
  description?: string
  cleanupPolicyIds?: string[]
  quotaBytes?: number | null
  blobStoreId?: string | null
  routingRuleId?: string | null
  proxyConfig?: Record<string, unknown> | null
  formatConfig?: Record<string, unknown> | null
}

/** Reads a string entry out of a repository config map, tolerating absent/typed-wrong values. */
function cfgString(cfg: Record<string, unknown> | null | undefined, key: string): string {
  const v = cfg?.[key]
  return typeof v === 'string' ? v : ''
}

/** Ordered member repository names of a group repo. */
function groupMemberNames(repo: Repository): string[] {
  const raw = repo.formatConfig?.['member_names']
  return Array.isArray(raw) ? raw.filter((n): n is string => typeof n === 'string') : []
}

interface BlobStoreLite {
  id: string
  name: string
  type: string
  quotaBytes?: number | null
  usedBytes?: number
}

interface CleanupPolicyRow {
  id: string
  name: string
  format: string
}

function cleanupPoliciesForFormat(policies: CleanupPolicyRow[], format: string) {
  return policies.filter(p => p.format === '*' || p.format === format)
}

const FORMAT_COLORS: Record<string, string> = {
  maven2:    '#f97316',
  npm:       '#ef4444',
  docker:    '#3b82f6',
  oci:       '#5b8def',
  pypi:      '#a78bfa',
  go:        '#06b6d4',
  nuget:     '#8b5cf6',
  helm:      '#0ea5e9',
  raw:       '#6b7280',
  apt:       '#f59e0b',
  yum:       '#10b981',
  conda:     '#44b765',
  rubygems:  '#e9573f',
  terraform: '#7b42bc',
  cran:      '#276dc3',
}

const TYPE_LABELS: Record<string, string> = {
  hosted: 'Hosted',
  proxy:  'Proxy',
  group:  'Group',
}

export default function RepositoriesPage() {
  const qc = useQueryClient()
  const navigate = useNavigate()
  const isAdmin = useAuthStore(s => s.isAdmin())
  const [filter, setFilter] = useState('')
  const [formatFilter, setFormatFilter] = useState('')
  const [showCreate, setShowCreate] = useState(false)
  const [editRepo, setEditRepo] = useState<Repository | null>(null)
  const [activeMigrations, setActiveMigrations] = React.useState<Set<string>>(new Set())

  const { data: repos = [], isLoading, isError, error, refetch } = useQuery<Repository[]>({
    queryKey: ['repositories', formatFilter],
    queryFn: () =>
      nexusApi.listRepositories(formatFilter ? { format: formatFilter } : {})
        .then(r => r.data),
  })

  const { data: blobStores = [] } = useQuery<BlobStoreLite[]>({
    queryKey: ['blobstores'],
    queryFn: () => nexusApi.listBlobStores().then(r => r.data),
  })
  const storeNameById = new Map(blobStores.map(b => [b.id, b.name]))

  const deleteMutation = useMutation({
    mutationFn: (name: string) => nexusApi.deleteRepository(name),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['repositories'] }),
  })

  const toggleOnlineMutation = useMutation({
    mutationFn: ({ name, online }: { name: string; online: boolean }) =>
      nexusApi.patchRepository(name, { online }),
    onMutate: async ({ name, online }) => {
      await qc.cancelQueries({ queryKey: ['repositories'] })
      const prev = qc.getQueryData<Repository[]>(['repositories', formatFilter])
      qc.setQueryData<Repository[]>(['repositories', formatFilter], old =>
        old?.map(r => r.name === name ? { ...r, online } : r) ?? old
      )
      return { prev }
    },
    onError: (_err, _vars, ctx) => {
      if (ctx?.prev) qc.setQueryData(['repositories', formatFilter], ctx.prev)
    },
    onSettled: () => qc.invalidateQueries({ queryKey: ['repositories'] }),
  })

  const filtered = repos.filter(r =>
    r.name.toLowerCase().includes(filter.toLowerCase()) ||
    (r.description ?? '').toLowerCase().includes(filter.toLowerCase())
  )

  return (
    <div className={styles.page}>
      <div style={{ marginBottom: 8 }}>
        <div className="holo-section-label" style={{ marginBottom: 4 }}>WORKSPACE / REPOSITORIES</div>
        <div style={{ display: 'flex', alignItems: 'flex-end', justifyContent: 'space-between', gap: 16 }}>
          <div>
            <h1 style={{ fontSize: 20, fontWeight: 700, margin: '0 0 3px', letterSpacing: '-0.01em', lineHeight: 1.2, background: 'linear-gradient(110deg, #7c5cff, #22d3ee 60%)', WebkitBackgroundClip: 'text', WebkitTextFillColor: 'transparent', backgroundClip: 'text' as const }}>Repositories</h1>
            <p style={{ fontSize: 12, color: 'var(--holo-text-faint)', margin: 0 }}>{repos.length} total</p>
          </div>
          <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
            <HoloButton icon={<RefreshCw size={15} />} onClick={() => refetch()} aria-label="Refresh" />
            {isAdmin && (
              <HoloButton variant="primary" icon={<Plus size={15} />} onClick={() => setShowCreate(true)}>
                Create Repository
              </HoloButton>
            )}
          </div>
        </div>
      </div>

      <div style={{ display: 'flex', gap: 12 }}>
        <HoloInput
          style={{ flex: 1 }}
          placeholder="Filter by name…"
          value={filter}
          onChange={e => setFilter(e.target.value)}
        />
        <Select
          options={[
            { value: '', label: 'All formats' },
            ...['maven2','npm','docker','oci','pypi','go','nuget','helm','raw','apt','yum','cargo','conan','conda','terraform','rubygems','cran'].map(f => ({ value: f, label: f })),
          ]}
          value={formatFilter}
          onChange={setFormatFilter}
          style={{ minWidth: 140 }}
        />
      </div>

      {isLoading ? (
        <div className={styles.empty}>Loading…</div>
      ) : isError ? (
        <div className={styles.empty}>
          <Database size={40} className={styles.emptyIcon} />
          <p style={{ color: '#ef4444', marginBottom: 8 }}>Error loading repositories</p>
          <p style={{ fontSize: 13, color: 'var(--holo-text-dim)', marginBottom: 16 }}>
            {error instanceof Error ? error.message : 'Unable to access repositories. Check your permissions or contact your administrator.'}
          </p>
          <HoloButton variant="primary" icon={<RefreshCw size={15} />} onClick={() => refetch()} style={{ marginTop: 8 }}>
            Retry
          </HoloButton>
        </div>
      ) : repos.length === 0 ? (
        <div className={styles.empty}>
          <Database size={40} className={styles.emptyIcon} />
          <p>No repositories found</p>
          <p style={{ fontSize: 12, color: 'var(--holo-text-dim)', marginTop: 8 }}>
            You don't have access to any repositories. Contact your administrator to grant you access.
          </p>
          {!filter && isAdmin && (
            <HoloButton variant="primary" icon={<Plus size={15} />} onClick={() => setShowCreate(true)}>
              Create your first repository
            </HoloButton>
          )}
        </div>
      ) : (
        <div className={styles.list}>
          {filtered.map(repo => (
            <RepoRow
              key={repo.id}
              repo={repo}
              isAdmin={isAdmin}
              storeName={repo.blobStoreId ? storeNameById.get(repo.blobStoreId) : undefined}
              migrating={activeMigrations.has(repo.name)}
              onClick={() => navigate(`/browse?repo=${repo.name}`)}
              onEdit={() => setEditRepo(repo)}
              onDelete={() => {
                if (confirm(`Delete repository "${repo.name}"?`)) {
                  deleteMutation.mutate(repo.name)
                }
              }}
              onToggleOnline={(online) => toggleOnlineMutation.mutate({ name: repo.name, online })}
              onExport={async () => {
                try {
                  const res = await nexspenceApi.exportRepo(repo.name)
                  const ts = new Date().toISOString().replace(/[:.]/g, '-').slice(0, 19)
                  const url = URL.createObjectURL(res.data as Blob)
                  const a = document.createElement('a')
                  a.href = url
                  a.download = `nexspence-repo-${repo.name}-${ts}.tar.gz`
                  a.click()
                  URL.revokeObjectURL(url)
                } catch {
                  // silent — no toast in this phase
                }
              }}
            />
          ))}
        </div>
      )}

      {showCreate && (
        <CreateRepoModal
          onClose={() => setShowCreate(false)}
          onCreated={() => {
            setShowCreate(false)
            qc.invalidateQueries({ queryKey: ['repositories'] })
          }}
        />
      )}

      {editRepo && (
        <EditRepoModal
          key={editRepo.id}
          repo={editRepo}
          onClose={() => setEditRepo(null)}
          onSaved={() => {
            qc.invalidateQueries({ queryKey: ['repositories'] })
            setEditRepo(null)
          }}
          onMigrationStarted={(repoName) =>
            setActiveMigrations(prev => new Set([...prev, repoName]))
          }
          onMigrationEnded={(repoName) => {
            setActiveMigrations(prev => {
              const next = new Set(prev)
              next.delete(repoName)
              return next
            })
            qc.invalidateQueries({ queryKey: ['repositories'] })
          }}
        />
      )}
    </div>
  )
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
  return `${(bytes / (1024 * 1024 * 1024)).toFixed(2)} GB`
}

function RepoRow({
  repo, isAdmin, storeName, migrating, onClick, onEdit, onDelete, onToggleOnline, onExport,
}: {
  repo: Repository
  isAdmin: boolean
  storeName?: string
  migrating?: boolean
  onClick?: () => void
  onEdit: () => void
  onDelete: () => void
  onToggleOnline: (online: boolean) => void
  onExport: () => void
}) {
  const { data: quota } = useQuery({
    queryKey: ['repoQuota', repo.name],
    queryFn: () => nexspenceApi.getRepositoryQuota(repo.name).then(r => r.data),
    staleTime: 30_000,
  })
  const pct = quota?.percentUsed ?? null

  return (
    <div className={styles.row} onClick={onClick} tabIndex={0} onKeyDown={(e) => {
      if (e.key === 'Enter' || e.key === ' ') {
        e.preventDefault()
        onClick?.()
      }
    }}>
      <span style={{
        width: 7, height: 7, borderRadius: '50%', flexShrink: 0,
        background: repo.online ? 'var(--holo-green)' : 'rgba(255,255,255,0.2)',
        boxShadow: repo.online ? '0 0 5px var(--holo-green)' : 'none',
        display: 'inline-block',
      }} />
      <span style={{
        fontSize: 10, fontWeight: 600, padding: '2px 8px', borderRadius: 4,
        textTransform: 'uppercase' as const, letterSpacing: '0.3px',
        background: (FORMAT_COLORS[repo.format] ?? '#6b7280') + '22',
        color: FORMAT_COLORS[repo.format] ?? '#6b7280',
        whiteSpace: 'nowrap' as const,
      }}>
        {repo.format}
      </span>
      <div style={{ minWidth: 0 }}>
        <Truncated text={repo.name} style={{ fontWeight: 600, fontSize: 13, color: 'var(--holo-text)' }} />
        {(repo.description || storeName) && (
          <Truncated
            text={repo.description || (storeName ? `on ${storeName}` : '')}
            style={{ fontSize: 11, color: 'var(--holo-text-faint)', marginTop: 2 }}
          />
        )}
        {repo.type === 'proxy' && cfgString(repo.proxyConfig, 'remote_url') && (
          <Truncated
            text={cfgString(repo.proxyConfig, 'remote_url')}
            style={{ fontSize: 11, color: 'var(--holo-text-faint)', fontFamily: 'ui-monospace,monospace', marginTop: 2 }}
          >
            <span aria-hidden="true">↗ </span>
            <span>{cfgString(repo.proxyConfig, 'remote_url')}</span>
          </Truncated>
        )}
      </div>
      <HoloPill tone={repo.type === 'hosted' ? 'success' : repo.type === 'proxy' ? 'default' : 'warn'}>
        {TYPE_LABELS[repo.type] ?? repo.type}
      </HoloPill>
      {migrating && (
        <span style={{
          fontSize: 10,
          padding: '1px 6px',
          borderRadius: 10,
          background: 'rgba(59,130,246,0.15)',
          border: '1px solid rgba(59,130,246,0.3)',
          color: '#60a5fa',
        }}>
          ⟳ migrating
        </span>
      )}
      <div>
        <div style={{ fontSize: 11, color: 'var(--holo-text-faint)', fontFamily: 'ui-monospace,monospace', textAlign: 'right' as const }}>
          {quota ? formatBytes(quota.usedBytes) : '—'}
        </div>
        {quota?.quotaBytes != null && (
          <div style={{ height: 3, background: 'rgba(255,255,255,0.08)', borderRadius: 2, overflow: 'hidden', marginTop: 3 }}>
            <div style={{
              height: '100%', borderRadius: 2,
              width: `${Math.min(pct ?? 0, 100)}%`,
              background: (pct ?? 0) >= 90 ? 'var(--holo-red)' : (pct ?? 0) >= 70 ? 'var(--holo-amber)' : 'var(--holo-green)',
            }} />
          </div>
        )}
      </div>
      {isAdmin && (
        <div style={{ display: 'flex', gap: 6, justifyContent: 'flex-end' }}>
          <HoloButton
            icon={<Power size={14} />}
            onClick={e => { e.stopPropagation(); onToggleOnline(!repo.online) }}
            title={repo.online ? 'Disable repository' : 'Enable repository'}
            style={{ color: repo.online ? 'var(--holo-green)' : 'var(--holo-text-faint)' }}
          />
          <HoloButton
            icon={<Download size={14} />}
            onClick={e => { e.stopPropagation(); onExport() }}
            title="Export repository"
          />
          <HoloButton icon={<Settings2 size={14} />} onClick={e => { e.stopPropagation(); onEdit() }} title="Settings" />
          <HoloButton variant="danger" icon={<Trash2 size={14} />} onClick={e => { e.stopPropagation(); onDelete() }} title="Delete" />
        </div>
      )}
    </div>
  )
}

// Default remote URLs per format for proxy repos
const PROXY_DEFAULTS: Record<string, string> = {
  maven2:    'https://repo1.maven.org/maven2/',
  npm:       'https://registry.npmjs.org/',
  pypi:      'https://pypi.org/',
  go:        'https://proxy.golang.org/',
  docker:    'https://registry-1.docker.io/',
  oci:       'https://ghcr.io/',
  helm:      'https://charts.bitnami.com/bitnami/',
  nuget:     'https://api.nuget.org/v3/',
  cargo:     'https://index.crates.io/',
  apt:       'http://archive.ubuntu.com/ubuntu/',
  yum:       'https://dl.fedoraproject.org/pub/epel/9/Everything/x86_64/',
  raw:       '',
  conda:     'https://conda.anaconda.org/conda-forge/',
  rubygems:  'https://rubygems.org/',
  terraform: 'https://registry.terraform.io/',
  cran:      'https://cran.r-project.org/',
}

/** Sets a trimmed value on cfg, or removes the key entirely when the field was cleared. */
function assignOrDelete(cfg: Record<string, unknown>, key: string, value: string) {
  const v = value.trim()
  if (v) cfg[key] = v
  else delete cfg[key]
}

const LABEL_STYLE = { fontSize: 12, fontWeight: 500, color: 'var(--holo-text-dim)', textTransform: 'uppercase' as const, letterSpacing: '0.4px' }
const ERROR_STYLE = { background: 'rgba(255,107,107,0.12)', border: '1px solid rgba(255,107,107,0.3)', borderRadius: 10, padding: '10px 12px', color: 'var(--holo-red)', fontSize: 13 }

function CreateRepoModal({ onClose, onCreated }: {
  onClose: () => void
  onCreated: () => void
}) {
  const { data: allRepos = [] } = useQuery<Repository[]>({
    queryKey: ['repositories'],
    queryFn: () => nexusApi.listRepositories({}).then(r => r.data),
  })
  const { data: cleanupPolicies = [] } = useQuery<CleanupPolicyRow[]>({
    queryKey: ['cleanupPolicies'],
    queryFn: () => nexusApi.listCleanupPolicies().then(r => r.data),
  })
  const { data: blobStores = [] } = useQuery<BlobStoreLite[]>({
    queryKey: ['blobstores'],
    queryFn: () => nexusApi.listBlobStores().then(r => r.data),
  })
  const { data: routingRules = [] } = useQuery<RoutingRule[]>({
    queryKey: ['routing-rules'],
    queryFn: () => nexusApi.listRoutingRules().then(r => r.data),
  })

  const defaultStoreId = blobStores.find(b => b.name === 'default')?.id ?? blobStores[0]?.id ?? ''

  const [form, setForm] = useState({
    name: '', format: 'maven2', type: 'hosted', description: '',
    remoteUrl: PROXY_DEFAULTS['maven2'],
    httpProxy: '', httpsProxy: '', socks5Proxy: '', noProxy: '',
    proxyUsername: '', proxyPassword: '',
    remoteUsername: '', remotePassword: '',
    minPackageAgeDays: '',
    memberNames: [] as string[],
    cleanupPolicyIds: [] as string[],
    quotaGB: '',
    allowAnonymous: false,
    blobStoreId: '',
    routingRuleId: '' as string,
  })
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  const setField = (field: string, value: unknown) =>
    setForm(f => ({ ...f, [field]: value }))

  const handleFormatChange = (fmt: string) => {
    setForm(f => ({
      ...f,
      format: fmt,
      remoteUrl: f.type === 'proxy' ? (PROXY_DEFAULTS[fmt] ?? '') : f.remoteUrl,
      cleanupPolicyIds: [],
    }))
  }

  const memberCandidates = allRepos.filter(
    r => r.format === form.format && r.type !== 'group'
  )
  const applicableCreate = cleanupPoliciesForFormat(cleanupPolicies, form.format)

  const validateStep = (stepIdx: number): boolean => {
    setError('')
    if (stepIdx === 1) {
      if (!form.name.trim()) { setError('Name is required'); return false }
      if (form.type === 'proxy' && !form.remoteUrl.trim()) { setError('Remote URL is required for proxy repositories'); return false }
      if (form.type === 'group' && form.memberNames.length === 0) { setError('Select at least one member repository'); return false }
    }
    if (stepIdx === 2) {
      const effectiveStoreId = form.type === 'group' ? '' : (form.blobStoreId || defaultStoreId)
      if (form.type !== 'group' && !effectiveStoreId) { setError('Select a blob store'); return false }
      const quotaValue = form.quotaGB.trim() !== '' ? parseFloat(form.quotaGB) : NaN
      if (!isNaN(quotaValue) && quotaValue > 0 && effectiveStoreId) {
        const store = blobStores.find(b => b.id === effectiveStoreId)
        if (store?.quotaBytes != null) {
          const repoBytes = Math.round(quotaValue * 1024 * 1024 * 1024)
          if (repoBytes > store.quotaBytes) {
            setError(`Repository quota (${formatBytes(repoBytes)}) exceeds blob store "${store.name}" quota (${formatBytes(store.quotaBytes)})`)
            return false
          }
        }
      }
    }
    return true
  }

  const handleFinish = async () => {
    setError('')
    const effectiveStoreId = form.type === 'group' ? '' : (form.blobStoreId || defaultStoreId)
    setLoading(true)
    try {
      const body: Record<string, unknown> = {
        name: form.name,
        description: form.description,
      }
      if (effectiveStoreId) body.blobStoreId = effectiveStoreId
      if (form.type === 'proxy') {
        const proxyConfig: Record<string, string | number> = { remote_url: form.remoteUrl.trim() }
        const ageDays = parseFloat(form.minPackageAgeDays)
        if (!isNaN(ageDays) && ageDays > 0) proxyConfig.minimum_package_age = Math.round(ageDays * 86400)
        if (form.httpProxy.trim()) proxyConfig.http_proxy = form.httpProxy.trim()
        if (form.httpsProxy.trim()) proxyConfig.https_proxy = form.httpsProxy.trim()
        if (form.socks5Proxy.trim()) proxyConfig.socks5_proxy = form.socks5Proxy.trim()
        if (form.noProxy.trim()) proxyConfig.no_proxy = form.noProxy.trim()
        if (form.proxyUsername.trim()) proxyConfig.proxy_username = form.proxyUsername.trim()
        if (form.proxyPassword) proxyConfig.proxy_password = form.proxyPassword
        if (form.remoteUsername.trim()) proxyConfig.remote_username = form.remoteUsername.trim()
        if (form.remotePassword) proxyConfig.remote_password = form.remotePassword
        body.proxyConfig = proxyConfig
      }
      if (form.type === 'group') body.formatConfig = { member_names: form.memberNames }
      if (form.type === 'group' && form.routingRuleId) {
        body.routingRuleId = form.routingRuleId
      }
      if (form.type !== 'group' && form.cleanupPolicyIds.length > 0) body.cleanupPolicyIds = form.cleanupPolicyIds
      if (form.quotaGB.trim() !== '') {
        const gb = parseFloat(form.quotaGB)
        if (!isNaN(gb) && gb > 0) body.quotaBytes = Math.round(gb * 1024 * 1024 * 1024)
      }
      body.allowAnonymous = form.allowAnonymous
      await apiClient.post(`/service/rest/v1/repositories/${form.format}/${form.type}`, body)
      onCreated()
    } catch (err) {
      setError(apiErrorMessage(err, 'Failed to create repository'))
    } finally {
      setLoading(false)
    }
  }

  const step1 = (
    <div className={styles.form}>
      <div className={styles.formRow}>
        <label style={LABEL_STYLE}>Format</label>
        <Select
          options={['maven2','npm','docker','oci','pypi','go','nuget','helm','raw','apt','yum','cargo','conan','conda','terraform','rubygems','cran'].map(f => ({ value: f, label: f }))}
          value={form.format}
          onChange={handleFormatChange}
        />
      </div>
      <div className={styles.formRow}>
        <label style={LABEL_STYLE}>Type</label>
        <Select
          options={[
            { value: 'hosted', label: 'Hosted — store artifacts locally' },
            { value: 'proxy',  label: 'Proxy — cache from remote registry' },
            { value: 'group',  label: 'Group — combine multiple repos' },
          ]}
          value={form.type}
          onChange={t => setForm(f => ({
            ...f,
            type: t,
            remoteUrl: t === 'proxy' ? (PROXY_DEFAULTS[f.format] ?? '') : '',
          }))}
        />
      </div>
    </div>
  )

  const step2 = (
    <div className={styles.form}>
      <div className={styles.formRow}>
        <label style={LABEL_STYLE}>Name *</label>
        <HoloInput
          value={form.name}
          onChange={e => setField('name', e.target.value)}
          placeholder="my-repo"
          autoFocus
        />
      </div>
      <div className={styles.formRow}>
        <label style={LABEL_STYLE}>Description</label>
        <HoloInput
          value={form.description}
          onChange={e => setField('description', e.target.value)}
          placeholder="Optional description"
        />
      </div>
      {form.type === 'proxy' && (
        <div className={styles.formRow}>
          <label style={LABEL_STYLE}>Remote URL *</label>
          <HoloInput
            type="url"
            value={form.remoteUrl}
            onChange={e => setField('remoteUrl', e.target.value)}
            placeholder="https://registry.example.com/"
          />
          <span className={styles.hint}>URL of the upstream registry to proxy and cache</span>
        </div>
      )}
      {form.type === 'proxy' && ['npm', 'pypi'].includes(form.format) && (
        <div className={styles.formRow}>
          <label style={LABEL_STYLE}>Minimum package age (days)</label>
          <HoloInput
            type="number"
            min="0"
            step="any"
            value={form.minPackageAgeDays}
            onChange={e => setField('minPackageAgeDays', e.target.value)}
            placeholder="e.g. 7"
          />
          <span className={styles.hint}>
            Supply-chain quarantine: upstream versions published more recently than this
            are hidden until they reach the configured age. Leave blank to disable.
          </span>
        </div>
      )}
      {form.type === 'proxy' && (
        <details className={styles.formRow}>
          <summary style={{ cursor: 'pointer', ...LABEL_STYLE }}>Upstream authentication (optional)</summary>
          <span className={styles.hint}>
            HTTP Basic credentials sent to the remote registry itself, for private
            upstreams that require authentication to download.
          </span>
          <div className={styles.formRow}>
            <label style={LABEL_STYLE}>Username</label>
            <HoloInput
              value={form.remoteUsername}
              onChange={e => setField('remoteUsername', e.target.value)}
              placeholder="Optional"
            />
          </div>
          <div className={styles.formRow}>
            <label style={LABEL_STYLE}>Password</label>
            <HoloInput
              type="password"
              value={form.remotePassword}
              onChange={e => setField('remotePassword', e.target.value)}
              placeholder="Optional"
            />
          </div>
        </details>
      )}
      {form.type === 'proxy' && (
        <details className={styles.formRow}>
          <summary style={{ cursor: 'pointer', ...LABEL_STYLE }}>Outbound proxy (optional)</summary>
          <span className={styles.hint}>
            Route upstream fetches through an HTTP or SOCKS5 proxy. Leave blank to use the
            server default (HTTP_PROXY/HTTPS_PROXY/NO_PROXY environment).
          </span>
          <div className={styles.formRow}>
            <label style={LABEL_STYLE}>HTTP proxy</label>
            <HoloInput
              value={form.httpProxy}
              onChange={e => setField('httpProxy', e.target.value)}
              placeholder="http://proxy.internal:3128"
            />
          </div>
          <div className={styles.formRow}>
            <label style={LABEL_STYLE}>HTTPS proxy</label>
            <HoloInput
              value={form.httpsProxy}
              onChange={e => setField('httpsProxy', e.target.value)}
              placeholder="http://secure-proxy.internal:3128"
            />
          </div>
          <div className={styles.formRow}>
            <label style={LABEL_STYLE}>SOCKS5 proxy</label>
            <HoloInput
              value={form.socks5Proxy}
              onChange={e => setField('socks5Proxy', e.target.value)}
              placeholder="socks5://proxy.internal:1080"
            />
          </div>
          <div className={styles.formRow}>
            <label style={LABEL_STYLE}>No proxy</label>
            <HoloInput
              value={form.noProxy}
              onChange={e => setField('noProxy', e.target.value)}
              placeholder="localhost,.internal.example.com"
            />
          </div>
          <div className={styles.formRow}>
            <label style={LABEL_STYLE}>Proxy username</label>
            <HoloInput
              value={form.proxyUsername}
              onChange={e => setField('proxyUsername', e.target.value)}
              placeholder="Optional"
            />
          </div>
          <div className={styles.formRow}>
            <label style={LABEL_STYLE}>Proxy password</label>
            <HoloInput
              type="password"
              value={form.proxyPassword}
              onChange={e => setField('proxyPassword', e.target.value)}
              placeholder="Optional"
            />
          </div>
        </details>
      )}
      {form.type === 'group' && (
        <div className={styles.formRow}>
          <label style={LABEL_STYLE}>Member Repositories *</label>
          {memberCandidates.length === 0 ? (
            <p className={styles.hint}>No {form.format} hosted/proxy repos found. Create them first.</p>
          ) : (
            <div className={styles.memberList}>
              {memberCandidates.map(r => (
                <label key={r.id} className={styles.memberItem}>
                  <input
                    type="checkbox"
                    checked={form.memberNames.includes(r.name)}
                    onChange={() =>
                      setField('memberNames',
                        form.memberNames.includes(r.name)
                          ? form.memberNames.filter(n => n !== r.name)
                          : [...form.memberNames, r.name]
                      )
                    }
                  />
                  <span className={styles.memberName}>{r.name}</span>
                  <span className={styles.memberType}>{r.type}</span>
                </label>
              ))}
            </div>
          )}
        </div>
      )}
      {form.type === 'group' && (
        <div style={{ marginTop: 12 }}>
          <label style={{ fontSize: 11, fontWeight: 600, color: 'var(--holo-text-dim)', display: 'block', marginBottom: 5 }}>
            ROUTING RULE
          </label>
          <Select
            value={form.routingRuleId}
            onChange={v => setField('routingRuleId', v)}
            options={[
              { value: '', label: 'None' },
              ...routingRules.map(r => ({ value: r.id, label: `${r.name} (${r.mode})` })),
            ]}
          />
        </div>
      )}
    </div>
  )

  const step3 = (
    <div className={styles.form}>
      {form.type !== 'group' && applicableCreate.length > 0 && (
        <div className={styles.formRow}>
          <label style={LABEL_STYLE}>Cleanup policies</label>
          <div className={styles.memberList}>
            {applicableCreate.map(p => (
              <label key={p.id} className={styles.memberItem}>
                <input
                  type="checkbox"
                  checked={form.cleanupPolicyIds.includes(p.id)}
                  onChange={() =>
                    setForm(f => ({
                      ...f,
                      cleanupPolicyIds: f.cleanupPolicyIds.includes(p.id)
                        ? f.cleanupPolicyIds.filter(x => x !== p.id)
                        : [...f.cleanupPolicyIds, p.id],
                    }))
                  }
                />
                <span className={styles.memberName}>{p.name}</span>
                <span className={styles.memberType}>{p.format === '*' ? 'all' : p.format}</span>
              </label>
            ))}
          </div>
        </div>
      )}
      {form.type !== 'group' && (
        <div className={styles.formRow}>
          <label style={LABEL_STYLE}>Blob Store *</label>
          {blobStores.length === 0 ? (
            <span className={styles.hint}>No blob stores configured. Create one in System Admin → Blob Stores.</span>
          ) : (
            <Select
              options={blobStores.map(b => ({ value: b.id, label: `${b.name} (${b.type})` }))}
              value={form.blobStoreId || defaultStoreId}
              onChange={v => setField('blobStoreId', v)}
            />
          )}
          {(() => {
            const sel = blobStores.find(b => b.id === (form.blobStoreId || defaultStoreId))
            if (!sel) return <span className={styles.hint}>Physical storage backend where artifacts are written.</span>
            if (sel.quotaBytes == null) return <span className={styles.hint}>Store quota: unlimited.</span>
            const free = sel.quotaBytes - (sel.usedBytes ?? 0)
            return <span className={styles.hint}>Store quota: {formatBytes(sel.quotaBytes)} · free {formatBytes(free)}</span>
          })()}
        </div>
      )}
      {form.type !== 'group' && (
        <div className={styles.formRow}>
          <label style={LABEL_STYLE}>Storage quota (GB)</label>
          <HoloInput
            type="number" min="0" step="0.1"
            value={form.quotaGB}
            onChange={e => setField('quotaGB', e.target.value)}
            placeholder="No limit"
          />
          <span className={styles.hint}>Leave blank for unlimited storage</span>
        </div>
      )}
      <div className={styles.formRow}>
        <label style={LABEL_STYLE}>Anonymous access</label>
        <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13, color: 'var(--holo-text)', cursor: 'pointer' }}>
          <input
            type="checkbox"
            checked={form.allowAnonymous}
            onChange={e => setField('allowAnonymous', e.target.checked)}
          />
          Allow unauthenticated read access
        </label>
        <span className={styles.hint}>When disabled, only users with an assigned role can read this repository.</span>
      </div>
    </div>
  )

  return (
    <Wizard
      steps={[
        { label: 'Type', content: step1 },
        { label: 'Settings', content: step2 },
        { label: 'Storage', content: step3 },
      ]}
      onFinish={handleFinish}
      finishLabel="Create"
      onValidateStep={validateStep}
      onClose={onClose}
      loading={loading}
      error={error}
    />
  )
}

function EditRepoModal({
  repo,
  onClose,
  onSaved,
  onMigrationStarted,
  onMigrationEnded,
}: {
  repo: Repository
  onClose: () => void
  onSaved: () => void
  onMigrationStarted?: (repoName: string) => void
  onMigrationEnded?: (repoName: string) => void
}) {
  const { data: policies = [] } = useQuery<CleanupPolicyRow[]>({
    queryKey: ['cleanupPolicies'],
    queryFn: () => nexusApi.listCleanupPolicies().then(r => r.data),
  })

  const { data: blobStores = [] } = useQuery<BlobStoreLite[]>({
    queryKey: ['blobstores'],
    queryFn: () => nexusApi.listBlobStores().then(r => r.data),
  })
  const { data: routingRules = [] } = useQuery<RoutingRule[]>({
    queryKey: ['routing-rules'],
    queryFn: () => nexusApi.listRoutingRules().then(r => r.data),
  })
  const { data: allRepos = [] } = useQuery<Repository[]>({
    queryKey: ['repositories'],
    queryFn: () => nexusApi.listRepositories({}).then(r => r.data),
    enabled: repo.type === 'group',
  })

  const applicable = cleanupPoliciesForFormat(policies, repo.format)
  const [description, setDescription] = useState(repo.description ?? '')
  const [online, setOnline] = useState(repo.online)
  const [allowAnonymous, setAllowAnonymous] = useState(repo.allowAnonymous ?? false)
  const [policyIds, setPolicyIds] = useState<string[]>(repo.cleanupPolicyIds ?? [])
  const [quotaGB, setQuotaGB] = useState(
    repo.quotaBytes != null ? String(repo.quotaBytes / (1024 * 1024 * 1024)) : ''
  )
  const [blobStoreId, setBlobStoreId] = useState<string>(repo.blobStoreId ?? '')
  const [routingRuleId, setRoutingRuleId] = useState<string>(repo.routingRuleId ?? '')
  const [remoteUrl, setRemoteUrl] = useState(cfgString(repo.proxyConfig, 'remote_url'))
  // Stored in seconds (proxy_config.minimum_package_age); the operator thinks
  // in days. Empty = policy disabled.
  const [minAgeDays, setMinAgeDays] = useState(() => {
    const raw = repo.proxyConfig?.['minimum_package_age']
    const secs = typeof raw === 'number' ? raw : typeof raw === 'string' ? parseFloat(raw) : NaN
    return !isNaN(secs) && secs > 0 ? String(secs / 86400) : ''
  })
  const [httpProxy, setHttpProxy] = useState(cfgString(repo.proxyConfig, 'http_proxy'))
  const [httpsProxy, setHttpsProxy] = useState(cfgString(repo.proxyConfig, 'https_proxy'))
  const [socks5Proxy, setSocks5Proxy] = useState(cfgString(repo.proxyConfig, 'socks5_proxy'))
  const [noProxy, setNoProxy] = useState(cfgString(repo.proxyConfig, 'no_proxy'))
  const [proxyUsername, setProxyUsername] = useState(cfgString(repo.proxyConfig, 'proxy_username'))
  // Passwords are never sent back by the API (see domain.RedactedRepository); an empty
  // field means "keep the stored one", so it starts blank regardless of what is stored.
  const [proxyPassword, setProxyPassword] = useState('')
  const [clearProxyPassword, setClearProxyPassword] = useState(false)
  const hasStoredProxyPassword = repo.proxyConfig?.['proxy_password_set'] === true
  const [remoteUsername, setRemoteUsername] = useState(cfgString(repo.proxyConfig, 'remote_username'))
  const [remotePassword, setRemotePassword] = useState('')
  const [clearRemotePassword, setClearRemotePassword] = useState(false)
  const hasStoredRemotePassword = repo.proxyConfig?.['remote_password_set'] === true
  const [memberNames, setMemberNames] = useState<string[]>(groupMemberNames(repo))
  const memberCandidates = allRepos.filter(r => r.format === repo.format && r.type !== 'group')
  const originalStoreId = repo.blobStoreId ?? ''
  const storeChanged = blobStoreId !== originalStoreId
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const [migration, setMigration] = React.useState<BlobStoreMigration | null>(null)
  const [migrLoading, setMigrLoading] = React.useState(false)
  const [migrError, setMigrError] = React.useState('')
  const pollingRef = React.useRef<ReturnType<typeof setInterval> | null>(null)

  const togglePolicy = (id: string) => {
    setPolicyIds(prev =>
      prev.includes(id) ? prev.filter(x => x !== id) : [...prev, id],
    )
  }

  const effectiveStoreId = blobStoreId || originalStoreId
  const selectedStore = blobStores.find(b => b.id === effectiveStoreId)

  const repoName = repo?.name
  React.useEffect(() => {
    if (!repoName) return
    getBlobStoreMigration(repoName)
      .then(m => setMigration(m))
      .catch(() => {})
  }, [repoName])

  const startPolling = React.useCallback((repoName: string) => {
    if (pollingRef.current) clearInterval(pollingRef.current)
    pollingRef.current = setInterval(async () => {
      try {
        const m = await getBlobStoreMigration(repoName)
        setMigration(m)
        if (m && (m.status === 'done' || m.status === 'failed' || m.status === 'cancelled')) {
          clearInterval(pollingRef.current!)
          pollingRef.current = null
          onMigrationEnded?.(repoName)
        }
      } catch { /* ignore */ }
    }, 2000)
  }, [onMigrationEnded])

  React.useEffect(() => () => { if (pollingRef.current) clearInterval(pollingRef.current) }, [])

  const handleMigrateContent = async () => {
    if (!repo) return
    const targetId = blobStoreId
    if (!targetId) return
    setMigrLoading(true)
    setMigrError('')
    try {
      const m = await startBlobStoreMigration(repo.name, targetId)
      setMigration(m)
      onMigrationStarted?.(repo.name)
      startPolling(repo.name)
    } catch (err) {
      setMigrError(apiErrorMessage(err, 'Failed to start migration'))
    } finally {
      setMigrLoading(false)
    }
  }

  const togglePolicyMember = (name: string) => {
    setMemberNames(prev =>
      prev.includes(name) ? prev.filter(n => n !== name) : [...prev, name],
    )
  }

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    setError('')

    if (repo.type === 'proxy' && !remoteUrl.trim()) {
      setError('Remote URL is required for proxy repositories')
      return
    }
    if (repo.type === 'group' && memberNames.length === 0) {
      setError('Select at least one member repository')
      return
    }

    // Enforce repo quota <= blob store quota (backend also checks).
    if (repo.type !== 'group' && quotaGB.trim() !== '') {
      const gb = parseFloat(quotaGB)
      if (!isNaN(gb) && gb > 0 && selectedStore?.quotaBytes != null) {
        const repoBytes = Math.round(gb * 1024 * 1024 * 1024)
        if (repoBytes > selectedStore.quotaBytes) {
          setError(`Repository quota (${formatBytes(repoBytes)}) exceeds blob store "${selectedStore.name}" quota (${formatBytes(selectedStore.quotaBytes)})`)
          return
        }
      }
    }

    setLoading(true)
    try {
      const updateBody: Record<string, unknown> = { description, online, allowAnonymous, cleanupPolicyIds: policyIds }
      if (quotaGB.trim() !== '') {
        const gb = parseFloat(quotaGB)
        if (!isNaN(gb) && gb > 0) {
          updateBody.quotaBytes = Math.round(gb * 1024 * 1024 * 1024)
        }
      } else {
        updateBody.quotaBytes = null
      }
      if (repo.type !== 'group' && blobStoreId) {
        updateBody.blobStoreId = blobStoreId
      }
      if (repo.type === 'group') {
        // Empty string, not null: the API reads an absent field as "unchanged"
        // and an empty one as "detach" (same convention as blobStoreId), so a
        // null here would silently leave the old rule attached.
        updateBody.routingRuleId = routingRuleId || ''
        // Preserve any other formatConfig entries (e.g. writable_member) the API set.
        const { proxy_password_set: _drop, ...rest } = (repo.formatConfig ?? {}) as Record<string, unknown>
        void _drop
        updateBody.formatConfig = { ...rest, member_names: memberNames }
      }
      if (repo.type === 'proxy') {
        const proxyConfig: Record<string, unknown> = {}
        for (const [k, v] of Object.entries(repo.proxyConfig ?? {})) {
          if (k !== 'proxy_password_set' && k !== 'remote_password_set') proxyConfig[k] = v
        }
        proxyConfig.remote_url = remoteUrl.trim()
        assignOrDelete(proxyConfig, 'http_proxy', httpProxy)
        assignOrDelete(proxyConfig, 'https_proxy', httpsProxy)
        assignOrDelete(proxyConfig, 'socks5_proxy', socks5Proxy)
        assignOrDelete(proxyConfig, 'no_proxy', noProxy)
        assignOrDelete(proxyConfig, 'proxy_username', proxyUsername)
        // Omitted proxy_password means "unchanged"; an explicit empty string clears it.
        if (proxyPassword) proxyConfig.proxy_password = proxyPassword
        else if (clearProxyPassword) proxyConfig.proxy_password = ''
        assignOrDelete(proxyConfig, 'remote_username', remoteUsername)
        // Same contract for the upstream registry password.
        if (remotePassword) proxyConfig.remote_password = remotePassword
        else if (clearRemotePassword) proxyConfig.remote_password = ''
        const ageDays = parseFloat(minAgeDays)
        if (!isNaN(ageDays) && ageDays > 0) proxyConfig.minimum_package_age = Math.round(ageDays * 86400)
        else delete proxyConfig.minimum_package_age
        updateBody.proxyConfig = proxyConfig
      }
      await nexusApi.updateRepository(repo.format, repo.type, repo.name, updateBody)
      onSaved()
    } catch (err: unknown) {
      const ax = err as { response?: { data?: { error?: string } } }
      setError(ax.response?.data?.error ?? 'Failed to save')
    } finally {
      setLoading(false)
    }
  }

  return (
    <HoloModal open={true} onClose={onClose} style={{ minWidth: 640 }} titleId="repo-modal-title">
      <h2 id="repo-modal-title" style={{ fontSize: 17, fontWeight: 700, color: 'var(--holo-text)', margin: 0 }}>Repository settings</h2>
      <form onSubmit={handleSubmit} className={styles.form}>
        <div className={styles.formRow}>
          <label style={LABEL_STYLE}>Name</label>
          <HoloInput value={repo.name} readOnly style={{ opacity: 0.55, cursor: 'not-allowed' }} />
        </div>
        <div className={styles.formRow}>
          <label style={LABEL_STYLE}>Online</label>
          <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13, color: 'var(--holo-text)', cursor: 'pointer' }}>
            <input
              type="checkbox"
              checked={online}
              onChange={e => setOnline(e.target.checked)}
            />
            Accept incoming requests
          </label>
        </div>
        <div className={styles.formRow}>
          <label style={LABEL_STYLE}>Anonymous access</label>
          <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13, color: 'var(--holo-text)', cursor: 'pointer' }}>
            <input
              type="checkbox"
              checked={allowAnonymous}
              onChange={e => setAllowAnonymous(e.target.checked)}
            />
            Allow unauthenticated read access
          </label>
          <span className={styles.hint}>When disabled, only users with an assigned role can read this repository.</span>
        </div>
        <div className={styles.formRow}>
          <label style={LABEL_STYLE}>Description</label>
          <HoloInput
            value={description}
            onChange={e => setDescription(e.target.value)}
            placeholder="Optional"
          />
        </div>
        {repo.type === 'proxy' && (
          <div className={styles.formRow}>
            <label style={LABEL_STYLE}>Remote URL *</label>
            <HoloInput
              type="url"
              value={remoteUrl}
              onChange={e => setRemoteUrl(e.target.value)}
              placeholder="https://registry.example.com/"
            />
            <span className={styles.hint}>
              URL of the upstream registry to proxy and cache. Already-cached artifacts are kept;
              new fetches go to the new upstream.
            </span>
          </div>
        )}
        {repo.type === 'proxy' && ['npm', 'pypi'].includes(repo.format) && (
          <div className={styles.formRow}>
            <label style={LABEL_STYLE}>Minimum package age (days)</label>
            <HoloInput
              type="number"
              min="0"
              step="any"
              value={minAgeDays}
              onChange={e => setMinAgeDays(e.target.value)}
              placeholder="e.g. 7"
            />
            <span className={styles.hint}>
              Supply-chain quarantine: upstream versions published more recently than this
              are hidden from metadata and refused on download until they reach the
              configured age. Leave blank to disable.
            </span>
          </div>
        )}
        {repo.type === 'proxy' && (
          <details className={styles.formRow}>
            <summary style={{ cursor: 'pointer', ...LABEL_STYLE }}>Upstream authentication (optional)</summary>
            <span className={styles.hint}>
              HTTP Basic credentials sent to the remote registry itself, for private
              upstreams that require authentication to download.
            </span>
            <div className={styles.formRow}>
              <label style={LABEL_STYLE}>Username</label>
              <HoloInput
                value={remoteUsername}
                onChange={e => setRemoteUsername(e.target.value)}
                placeholder="Optional"
              />
            </div>
            <div className={styles.formRow}>
              <label style={LABEL_STYLE}>Password</label>
              <HoloInput
                type="password"
                value={remotePassword}
                onChange={e => { setRemotePassword(e.target.value); setClearRemotePassword(false) }}
                placeholder="Leave blank to keep current"
              />
              {hasStoredRemotePassword && (
                <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13, color: 'var(--holo-text)', cursor: 'pointer', marginTop: 6 }}>
                  <input
                    type="checkbox"
                    checked={clearRemotePassword}
                    disabled={remotePassword !== ''}
                    onChange={e => setClearRemotePassword(e.target.checked)}
                  />
                  Remove the stored password
                </label>
              )}
              <span className={styles.hint}>
                {hasStoredRemotePassword
                  ? 'A password is stored. It is never sent to the browser — leave this blank to keep it.'
                  : 'No upstream password stored for this repository.'}
              </span>
            </div>
          </details>
        )}
        {repo.type === 'proxy' && (
          <details className={styles.formRow}>
            <summary style={{ cursor: 'pointer', ...LABEL_STYLE }}>Outbound proxy (optional)</summary>
            <span className={styles.hint}>
              Route upstream fetches through an HTTP or SOCKS5 proxy. Leave blank to use the
              server default (HTTP_PROXY/HTTPS_PROXY/NO_PROXY environment).
            </span>
            <div className={styles.formRow}>
              <label style={LABEL_STYLE}>HTTP proxy</label>
              <HoloInput
                value={httpProxy}
                onChange={e => setHttpProxy(e.target.value)}
                placeholder="http://proxy.internal:3128"
              />
            </div>
            <div className={styles.formRow}>
              <label style={LABEL_STYLE}>HTTPS proxy</label>
              <HoloInput
                value={httpsProxy}
                onChange={e => setHttpsProxy(e.target.value)}
                placeholder="http://secure-proxy.internal:3128"
              />
            </div>
            <div className={styles.formRow}>
              <label style={LABEL_STYLE}>SOCKS5 proxy</label>
              <HoloInput
                value={socks5Proxy}
                onChange={e => setSocks5Proxy(e.target.value)}
                placeholder="socks5://proxy.internal:1080"
              />
            </div>
            <div className={styles.formRow}>
              <label style={LABEL_STYLE}>No proxy</label>
              <HoloInput
                value={noProxy}
                onChange={e => setNoProxy(e.target.value)}
                placeholder="localhost,.internal.example.com"
              />
            </div>
            <div className={styles.formRow}>
              <label style={LABEL_STYLE}>Proxy username</label>
              <HoloInput
                value={proxyUsername}
                onChange={e => setProxyUsername(e.target.value)}
                placeholder="Optional"
              />
            </div>
            <div className={styles.formRow}>
              <label style={LABEL_STYLE}>Proxy password</label>
              <HoloInput
                type="password"
                value={proxyPassword}
                onChange={e => { setProxyPassword(e.target.value); setClearProxyPassword(false) }}
                placeholder="Leave blank to keep current"
              />
              {hasStoredProxyPassword && (
                <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13, color: 'var(--holo-text)', cursor: 'pointer', marginTop: 6 }}>
                  <input
                    type="checkbox"
                    checked={clearProxyPassword}
                    disabled={proxyPassword !== ''}
                    onChange={e => setClearProxyPassword(e.target.checked)}
                  />
                  Remove the stored password
                </label>
              )}
              <span className={styles.hint}>
                {hasStoredProxyPassword
                  ? 'A password is stored. It is never sent to the browser — leave this blank to keep it.'
                  : 'No password stored for this repository.'}
              </span>
            </div>
          </details>
        )}
        {repo.type === 'group' && (
          <div className={styles.formRow}>
            <label style={LABEL_STYLE}>Member Repositories *</label>
            {memberCandidates.length === 0 ? (
              <p className={styles.hint}>No {repo.format} hosted/proxy repos found. Create them first.</p>
            ) : (
              <div className={styles.memberList}>
                {memberCandidates.map(r => (
                  <label key={r.id} className={styles.memberItem}>
                    <input
                      type="checkbox"
                      checked={memberNames.includes(r.name)}
                      onChange={() => togglePolicyMember(r.name)}
                    />
                    <span className={styles.memberName}>{r.name}</span>
                    <span className={styles.memberType}>{r.type}</span>
                  </label>
                ))}
              </div>
            )}
            <span className={styles.hint}>Members are searched in the order shown; the first hit wins.</span>
          </div>
        )}
        {repo.type !== 'group' && blobStores.length > 0 && (
          <div className={styles.formRow}>
            <label style={LABEL_STYLE}>Blob Store</label>
            <Select
              options={blobStores.map(b => ({ value: b.id, label: `${b.name} (${b.type})` }))}
              value={blobStoreId || originalStoreId}
              onChange={setBlobStoreId}
            />
            {storeChanged ? (
              <span className={styles.hint} style={{ color: '#f59e0b' }}>
                ⚠ Existing artifacts stay on the original store. Only future uploads land on the new one.
              </span>
            ) : (
              <span className={styles.hint}>Physical storage backend for new uploads.</span>
            )}
            {selectedStore && selectedStore.quotaBytes != null && (
              <span className={styles.hint}>
                Store quota: {formatBytes(selectedStore.quotaBytes)} · free {formatBytes(selectedStore.quotaBytes - (selectedStore.usedBytes ?? 0))}
              </span>
            )}
            {/* Migrate Content button — shown when store differs and no active migration */}
            {storeChanged && (!migration || migration.status === 'cancelled' || migration.status === 'failed') && (
              <div style={{ marginTop: 8 }}>
                <button
                  type="button"
                  className="holo-btn"
                  onClick={handleMigrateContent}
                  disabled={migrLoading}
                  style={{ fontSize: 12 }}
                >
                  {migrLoading ? 'Starting…' : 'Migrate Content'}
                </button>
                {migrError && (
                  <p role="alert" style={{ color: '#ef4444', fontSize: 12, marginTop: 4 }}>{migrError}</p>
                )}
              </div>
            )}
            {/* Progress section */}
            {migration && migration.status !== 'done' && (
              <div style={{
                marginTop: 12,
                padding: '10px 12px',
                background: 'rgba(59,130,246,0.06)',
                border: '1px solid rgba(59,130,246,0.2)',
                borderRadius: 8,
              }}>
                <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 6 }}>
                  <span style={{ fontSize: 12, color: '#94a3b8' }}>
                    {migration.status === 'running' || migration.status === 'pending'
                      ? 'Migrating content…'
                      : migration.status === 'cancelled' ? 'Migration cancelled'
                      : `Migration failed: ${migration.errorMessage ?? 'unknown error'}`}
                  </span>
                  {(migration.status === 'running' || migration.status === 'pending') && (
                    <button
                      type="button"
                      className="holo-btn holo-btn--danger"
                      style={{ fontSize: 11, padding: '2px 8px' }}
                      onClick={() => cancelBlobStoreMigration(repo!.name).catch(() => {})}
                    >
                      Cancel
                    </button>
                  )}
                </div>
                {migration.totalAssets > 0 && (
                  <>
                    <div style={{ height: 4, borderRadius: 2, background: 'rgba(255,255,255,0.1)', overflow: 'hidden', marginBottom: 4 }}>
                      <div style={{
                        height: '100%',
                        width: `${Math.round((migration.doneAssets / migration.totalAssets) * 100)}%`,
                        background: migration.status === 'failed' ? '#ef4444' : '#3b82f6',
                        transition: 'width 0.3s ease',
                      }} />
                    </div>
                    <div style={{ fontSize: 11, color: '#64748b' }}>
                      {migration.doneAssets} / {migration.totalAssets} assets · {formatBytes(migration.doneBytes)} / {formatBytes(migration.totalBytes)}
                    </div>
                  </>
                )}
              </div>
            )}
            {migration?.status === 'done' && (
              <div style={{ marginTop: 8, fontSize: 12, color: '#22c55e' }}>
                ✓ Migration complete — content is now on the new store
              </div>
            )}
          </div>
        )}

        {repo.type !== 'group' && (
          <div className={styles.formRow}>
            <label style={LABEL_STYLE}>Storage quota (GB)</label>
            <HoloInput
              type="number"
              min="0"
              step="0.1"
              value={quotaGB}
              onChange={e => setQuotaGB(e.target.value)}
              placeholder="No limit"
            />
            <span className={styles.hint}>Leave blank to remove the quota limit</span>
          </div>
        )}
        <div className={styles.formRow}>
          <label style={LABEL_STYLE}>Cleanup policies</label>
          {applicable.length === 0 ? (
            <p className={styles.hint}>Create policies on the Cleanup page first.</p>
          ) : (
            <div className={styles.memberList}>
              {applicable.map(p => (
                <label key={p.id} className={styles.memberItem}>
                  <input
                    type="checkbox"
                    checked={policyIds.includes(p.id)}
                    onChange={() => togglePolicy(p.id)}
                  />
                  <span className={styles.memberName}>{p.name}</span>
                  <span className={styles.memberType}>{p.format === '*' ? 'all' : p.format}</span>
                </label>
              ))}
            </div>
          )}
          <span className={styles.hint}>Scheduled and manual runs only affect attached repositories.</span>
        </div>
        {repo.type === 'group' && (
          <div className={styles.formRow}>
            <label style={LABEL_STYLE}>Routing Rule</label>
            <Select
              value={routingRuleId}
              onChange={setRoutingRuleId}
              options={[
                { value: '', label: 'None' },
                ...routingRules.map(r => ({ value: r.id, label: `${r.name} (${r.mode})` })),
              ]}
            />
            <span className={styles.hint}>Route requests through this routing rule before dispatching to members.</span>
          </div>
        )}
        {error && <div style={ERROR_STYLE}>{error}</div>}
        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 10, marginTop: 8 }}>
          <HoloButton type="button" onClick={onClose}>Cancel</HoloButton>
          <HoloButton variant="primary" type="submit" disabled={loading}>
            {loading ? 'Saving…' : 'Save'}
          </HoloButton>
        </div>
      </form>
    </HoloModal>
  )
}
