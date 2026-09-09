import { type ChangeEvent, type CSSProperties, useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Trash2, RefreshCw, Plus, Play, Pencil, X, Check, AlertCircle, Folder, Search } from 'lucide-react'
import { nexusApi, apiErrorMessage } from '@/api/client'
import type { CleanupPreviewResponse } from '@/api/client'
import { Select } from '../components/Select'
import { HoloButton, HoloInput, HoloModal, HoloPill, Wizard } from '@/components/holo'
import { Truncated } from '@/components/Truncated'

interface CleanupScope {
  repositoryName?: string
  pathPrefix?: string
}

interface CleanupPolicy {
  id: string
  name: string
  description?: string
  format: string
  criteria: Record<string, number | string>
  scheduleCron?: string
  enabled: boolean
  dryRun: boolean
  retainNVersions?: number
  scope?: CleanupScope
  lastRunAt?: string
  lastRunFreedBytes?: number
  lastRunCount?: number
}

interface PolicyForm {
  name: string
  description: string
  format: string
  enabled: boolean
  dryRun: boolean
  lastDownloadedDays: string
  artifactAgeDays: string
  pathPrefix: string
  nameGlob: string
  scheduleCron: string
  retainNVersions: string
  scopeRepository: string
  scopePath: string
}

const FORMATS = ['*', 'maven2', 'npm', 'docker', 'oci', 'pypi', 'go', 'nuget', 'helm', 'raw', 'apt', 'yum', 'cargo', 'conan', 'cran', 'alpine']

const FORMAT_COLOR: Record<string, string> = {
  maven2: '#f97316', npm: '#ef4444', docker: '#3b82f6', oci: '#5b8def', pypi: '#a78bfa',
  go: '#06b6d4', nuget: '#8b5cf6', helm: '#0ea5e9', raw: '#6b7280',
  apt: '#f59e0b', yum: '#10b981', cargo: '#fb923c', conan: '#94a3b8',
  cran: '#276dc3', alpine: '#0d597f',
  '*': '#6b7280',
}

const emptyForm = (): PolicyForm => ({
  name: '', description: '', format: '*',
  enabled: true, dryRun: false,
  lastDownloadedDays: '', artifactAgeDays: '',
  pathPrefix: '', nameGlob: '',
  scheduleCron: '',
  retainNVersions: '',
  scopeRepository: '',
  scopePath: '',
})

function fmtBytes(b: number) {
  if (b >= 1e9) return (b / 1e9).toFixed(1) + ' GB'
  if (b >= 1e6) return (b / 1e6).toFixed(1) + ' MB'
  if (b >= 1e3) return (b / 1e3).toFixed(1) + ' KB'
  return b + ' B'
}

// ── PathBrowserModal ───────────────────────────────────────────────────────────

function PathBrowserModal({ repoName, current, onSelect, onClose }: {
  repoName: string
  current: string
  onSelect: (path: string) => void
  onClose: () => void
}) {
  const [selected, setSelected] = useState(current)
  const [filter, setFilter] = useState('')

  const { data, isLoading, isError } = useQuery<{ paths: string[] }>({
    queryKey: ['pathTree', repoName],
    queryFn: () => nexusApi.listPathTree(repoName).then(r => r.data),
  })

  const paths = data?.paths ?? []
  const visible = filter.trim()
    ? paths.filter(p => p.toLowerCase().includes(filter.toLowerCase()))
    : paths

  return (
    <HoloModal open={true} onClose={onClose} style={{ minWidth: 480, maxWidth: 640 }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <h2 style={{ fontSize: 15, fontWeight: 700, color: 'var(--holo-text)', margin: 0 }}>
          Browse — {repoName}
        </h2>
        <HoloButton onClick={onClose} style={{ padding: 4 }}><X size={15} /></HoloButton>
      </div>

      <div style={{ position: 'relative' }}>
        <Search size={13} style={{ position: 'absolute', left: 10, top: '50%', transform: 'translateY(-50%)', color: 'var(--holo-text-faint)', pointerEvents: 'none' }} />
        <HoloInput
          value={filter}
          onChange={e => setFilter(e.target.value)}
          placeholder="Filter paths…"
          style={{ width: '100%', boxSizing: 'border-box', paddingLeft: 30 }}
          autoFocus
        />
      </div>

      <div style={{ maxHeight: 320, overflowY: 'auto', display: 'flex', flexDirection: 'column', gap: 2 }}>
        {isLoading && (
          <div style={{ color: 'var(--holo-text-faint)', fontSize: 13, padding: '12px 0', textAlign: 'center' }}>Loading…</div>
        )}
        {isError && (
          <div style={{ color: 'var(--holo-red)', fontSize: 12, padding: 8 }}>Failed to load path tree.</div>
        )}
        {!isLoading && !isError && visible.length === 0 && (
          <div style={{ color: 'var(--holo-text-faint)', fontSize: 13, padding: '12px 0', textAlign: 'center' }}>No paths found.</div>
        )}
        {visible.map(path => (
          <div
            key={path}
            onClick={() => setSelected(path)}
            style={{
              display: 'flex', alignItems: 'center', gap: 8,
              padding: '7px 10px', borderRadius: 8, cursor: 'pointer',
              fontSize: 12, fontFamily: 'monospace',
              color: selected === path ? 'var(--holo-b)' : 'var(--holo-text)',
              background: selected === path ? 'rgba(34,211,238,0.10)' : 'transparent',
              border: selected === path ? '1px solid rgba(34,211,238,0.25)' : '1px solid transparent',
              transition: 'background 0.1s',
            }}
            onMouseEnter={e => { if (selected !== path) (e.currentTarget as HTMLDivElement).style.background = 'rgba(255,255,255,0.04)' }}
            onMouseLeave={e => { (e.currentTarget as HTMLDivElement).style.background = selected === path ? 'rgba(34,211,238,0.10)' : 'transparent' }}
          >
            <Folder size={13} style={{ color: 'var(--holo-text-faint)', flexShrink: 0 }} />
            {path}
          </div>
        ))}
      </div>

      <div style={{ display: 'flex', gap: 10, justifyContent: 'flex-end' }}>
        <HoloButton onClick={onClose}>Cancel</HoloButton>
        <HoloButton
          variant="primary"
          disabled={!selected}
          onClick={() => { onSelect(selected); onClose() }}
        >
          Select {selected ? `"${selected}"` : ''}
        </HoloButton>
      </div>
    </HoloModal>
  )
}

// ── PreviewModal ───────────────────────────────────────────────────────────────

function PreviewModal({ policyId, policyName, onClose, onRun }: {
  policyId: string
  policyName: string
  onClose: () => void
  onRun: () => void
}) {
  const { data, isLoading, isError, error } = useQuery<CleanupPreviewResponse>({
    queryKey: ['cleanupPreview', policyId],
    queryFn: () => nexusApi.previewCleanupPolicy(policyId).then(r => r.data),
  })

  return (
    <HoloModal open={true} onClose={onClose} style={{ minWidth: 640, maxWidth: '90vw' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <div>
          <h2 style={{ fontSize: 17, fontWeight: 700, color: 'var(--holo-text)', margin: '0 0 2px' }}>
            Dry Run Preview
          </h2>
          <div style={{ fontSize: 12, color: 'var(--holo-text-faint)' }}>
            Policy: {policyName} · no actual deletes
          </div>
        </div>
        <HoloButton onClick={onClose} style={{ padding: 4 }}><X size={15} /></HoloButton>
      </div>

      {isLoading && (
        <div style={{ color: 'var(--holo-text-faint)', fontSize: 13, textAlign: 'center', padding: '24px 0' }}>
          Computing preview…
        </div>
      )}

      {isError && (
        <div role="alert" style={{ display: 'flex', gap: 8, alignItems: 'center', fontSize: 12, color: 'var(--holo-red)', background: 'rgba(255,107,107,0.08)', border: '1px solid rgba(255,107,107,0.25)', borderRadius: 8, padding: '10px 14px' }}>
          <AlertCircle size={14} />
          {apiErrorMessage(error, 'Preview failed')}
        </div>
      )}

      {data && data.totalCount === 0 && (
        <div style={{ color: 'var(--holo-text-faint)', fontSize: 13, textAlign: 'center', padding: '24px 0' }}>
          Nothing matches the policy criteria.
        </div>
      )}

      {data && data.totalCount > 0 && (
        <>
          {/* Stats row */}
          <div style={{ display: 'flex', gap: 16, alignItems: 'center', flexWrap: 'wrap' }}>
            <span style={{ fontWeight: 700, fontSize: 13, color: 'var(--holo-red)' }}>
              {data.totalCount} assets to delete
            </span>
            <span style={{ color: 'var(--holo-text-faint)', fontSize: 12 }}>·</span>
            <span style={{ fontWeight: 700, fontSize: 13, color: 'var(--holo-green)' }}>
              {fmtBytes(data.totalBytes)} to free
            </span>
          </div>

          {/* Table */}
          <div style={{ overflowX: 'auto', maxHeight: 360, overflowY: 'auto' }}>
            <table className="holo-table" style={{ width: '100%' }}>
              <thead>
                <tr>
                  <th>Path</th>
                  <th>Repository</th>
                  <th>Size</th>
                  <th>Last downloaded</th>
                  <th>Created</th>
                  <th>Reason</th>
                </tr>
              </thead>
              <tbody>
                {data.assets.map((a, i) => (
                  <tr key={i}>
                    <Truncated as="td" text={a.path} style={{ fontFamily: 'monospace', fontSize: 11, color: 'var(--holo-b)', maxWidth: 240 }} />
                    <td style={{ fontSize: 12, color: 'var(--holo-text-dim)' }}>{a.repository}</td>
                    <td style={{ fontSize: 12, color: 'var(--holo-amber)', whiteSpace: 'nowrap' }}>{fmtBytes(a.sizeBytes)}</td>
                    <td>
                      {a.lastDownloaded
                        ? <span style={{ fontSize: 11, color: 'var(--holo-text-faint)' }}>{new Date(a.lastDownloaded).toLocaleDateString()}</span>
                        : <HoloPill tone="danger" style={{ fontSize: 10 }}>never</HoloPill>
                      }
                    </td>
                    <td style={{ fontSize: 11, color: 'var(--holo-text-faint)', whiteSpace: 'nowrap' }}>
                      {new Date(a.createdAt).toLocaleDateString()}
                    </td>
                    <td style={{ fontSize: 11, color: 'var(--holo-text-dim)' }}>{a.reason}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          <div style={{ fontSize: 11, color: 'var(--holo-text-faint)' }}>
            Showing {data.assets.length} of {data.totalCount} · No actual deletes occurred
          </div>
        </>
      )}

      <div style={{ display: 'flex', gap: 10, justifyContent: 'flex-end' }}>
        <HoloButton onClick={onClose}>Close</HoloButton>
        <HoloButton
          variant="primary"
          icon={<Play size={13} />}
          onClick={() => { onRun(); onClose() }}
        >
          Run for real
        </HoloButton>
      </div>
    </HoloModal>
  )
}

// ── PolicyModal ────────────────────────────────────────────────────────────────

function PolicyModal({
  initial, onClose, onSaved,
}: { initial?: CleanupPolicy | null; onClose: () => void; onSaved: () => void }) {
  const [form, setForm] = useState<PolicyForm>(() => {
    if (!initial) return emptyForm()
    return {
      name: initial.name,
      description: initial.description ?? '',
      format: initial.format,
      enabled: initial.enabled,
      dryRun: initial.dryRun,
      lastDownloadedDays: String(initial.criteria?.lastDownloadedDays ?? ''),
      artifactAgeDays: String(initial.criteria?.artifactAgeDays ?? ''),
      pathPrefix: String(initial.criteria?.pathPrefix ?? ''),
      nameGlob: String(initial.criteria?.nameGlob ?? ''),
      scheduleCron: initial.scheduleCron ?? '',
      retainNVersions: String(initial.retainNVersions ?? ''),
      scopeRepository: initial.scope?.repositoryName ?? '',
      scopePath: initial.scope?.pathPrefix ?? '',
    }
  })
  const [err, setErr] = useState('')
  const [wizardError, setWizardError] = useState('')
  const [wizardLoading, setWizardLoading] = useState(false)
  const [showBrowser, setShowBrowser] = useState(false)

  // Fetch repos for the scope picker. Available for every format, including '*'
  // (a clear-all policy still needs to target a specific repository).
  const { data: reposData } = useQuery<{ name: string; format: string }[]>({
    queryKey: ['repositories'],
    queryFn: () => nexusApi.listRepositories().then(r => r.data),
  })
  const filteredRepos = form.format === '*'
    ? (reposData ?? [])
    : (reposData ?? []).filter(r => r.format === form.format)

  const payload = () => ({
    name: form.name.trim(),
    description: form.description.trim(),
    format: form.format,
    enabled: form.enabled,
    dryRun: form.dryRun,
    scheduleCron: form.scheduleCron.trim(),
    retainNVersions: form.retainNVersions ? Number(form.retainNVersions) : 0,
    criteria: {
      ...(form.lastDownloadedDays ? { lastDownloadedDays: Number(form.lastDownloadedDays) } : {}),
      ...(form.artifactAgeDays ? { artifactAgeDays: Number(form.artifactAgeDays) } : {}),
      ...(form.pathPrefix.trim() ? { pathPrefix: form.pathPrefix.trim() } : {}),
      ...(form.nameGlob.trim() ? { nameGlob: form.nameGlob.trim() } : {}),
    },
    scope: {
      ...(form.scopeRepository ? { repositoryName: form.scopeRepository } : {}),
      ...(form.scopePath ? { pathPrefix: form.scopePath } : {}),
    },
  })

  const handleSave = async () => {
    if (!form.name.trim()) { setErr('Name is required'); return }
    try {
      if (initial) {
        await nexusApi.updateCleanupPolicy(initial.id, payload())
      } else {
        await nexusApi.createCleanupPolicy(payload())
      }
      onSaved()
      onClose()
    } catch (e) {
      setErr(apiErrorMessage(e, 'Save failed'))
    }
  }

  const set = (k: keyof PolicyForm) => (e: ChangeEvent<HTMLInputElement | HTMLSelectElement>) =>
    setForm(f => ({ ...f, [k]: e.target.value }))

  const LABEL = { fontSize: 12, fontWeight: 600 as const, color: 'var(--holo-text-dim)', textTransform: 'uppercase' as const, letterSpacing: '0.04em' }

  // Scope section — shared between wizard and edit modal. Shown for all formats
  // (including '*') so a clear-all policy can be pinned to a repository.
  const scopeSection = (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
      {/* Divider */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginTop: 4 }}>
        <div style={{ flex: 1, height: 1, background: 'rgba(124,92,255,0.2)' }} />
        <span style={{ fontSize: 10, fontWeight: 600, color: 'var(--holo-text-faint)', textTransform: 'uppercase', letterSpacing: '0.08em' }}>Scope</span>
        <div style={{ flex: 1, height: 1, background: 'rgba(124,92,255,0.2)' }} />
      </div>

      {/* Repository picker */}
      <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
        <label style={LABEL}>Target repository</label>
        <Select
          options={[
            { value: '', label: '— All repositories —' },
            ...filteredRepos.map(r => ({
              value: r.name,
              label: r.name,
              badge: (
                <span style={{ width: 7, height: 7, borderRadius: '50%', background: FORMAT_COLOR[form.format] ?? '#6b7280', flexShrink: 0, display: 'inline-block' }} />
              ),
            })),
          ]}
          value={form.scopeRepository}
          onChange={v => setForm(f => ({ ...f, scopeRepository: v, scopePath: v ? f.scopePath : '' }))}
        />
        {form.scopeRepository && (
          <div style={{ display: 'flex', gap: 6, alignItems: 'center', marginTop: 2 }}>
            <span style={{
              display: 'inline-flex', alignItems: 'center', gap: 5,
              fontSize: 11, padding: '2px 8px', borderRadius: 999,
              background: (FORMAT_COLOR[form.format] ?? '#6b7280') + '22',
              color: FORMAT_COLOR[form.format] ?? '#6b7280',
              border: `1px solid ${FORMAT_COLOR[form.format] ?? '#6b7280'}44`,
            }}>
              <span style={{ width: 6, height: 6, borderRadius: '50%', background: FORMAT_COLOR[form.format] ?? '#6b7280', display: 'inline-block' }} />
              {form.scopeRepository}
              <button
                type="button"
                onClick={() => setForm(f => ({ ...f, scopeRepository: '', scopePath: '' }))}
                style={{ background: 'none', border: 'none', cursor: 'pointer', padding: 0, color: 'inherit', display: 'flex', alignItems: 'center', marginLeft: 2 }}
              >
                <X size={10} />
              </button>
            </span>
          </div>
        )}
      </div>

      {/* Path input + Browse button */}
      <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
        <label style={LABEL}>Path prefix</label>
        <div style={{ display: 'flex', gap: 8 }}>
          <HoloInput
            value={form.scopePath}
            onChange={set('scopePath')}
            placeholder="e.g. /releases/ (leave empty for all paths)"
            disabled={!form.scopeRepository}
            style={{ flex: 1 }}
          />
          <HoloButton
            onClick={() => setShowBrowser(true)}
            disabled={!form.scopeRepository}
            title="Browse repository paths"
          >
            Browse…
          </HoloButton>
        </div>
        <span style={{ fontSize: 11, color: 'rgba(229,231,235,0.35)' }}>
          Leave empty to match all paths in the repository.
        </span>
      </div>

      {/* Info banner */}
      {form.scopeRepository && form.scopePath && (
        <div style={{
          display: 'flex', alignItems: 'flex-start', gap: 8,
          fontSize: 12, color: 'var(--holo-b)',
          background: 'rgba(34,211,238,0.08)',
          border: '1px solid rgba(34,211,238,0.22)',
          borderRadius: 8, padding: '9px 12px',
        }}>
          <span style={{ fontSize: 14, flexShrink: 0 }}>ℹ</span>
          <span>
            This policy will target <strong>{form.scopeRepository}</strong> at path{' '}
            <code style={{ fontFamily: 'monospace', fontSize: 11 }}>{form.scopePath}</code>{' '}
            and all sub-paths.
          </span>
        </div>
      )}
    </div>
  )

  // ── Create mode: stepped wizard ───────────────────────────────────────
  if (!initial) {
    const validateStep = (stepIdx: number): boolean => {
      setWizardError('')
      if (stepIdx === 0 && !form.name.trim()) {
        setWizardError('Name is required')
        return false
      }
      return true
    }

    const handleFinish = async () => {
      setWizardError('')
      if (!form.name.trim()) { setWizardError('Name is required'); return }
      setWizardLoading(true)
      try {
        await nexusApi.createCleanupPolicy(payload())
        onSaved()
        onClose()
      } catch (e) {
        setWizardError(apiErrorMessage(e, 'Save failed'))
      } finally {
        setWizardLoading(false)
      }
    }

    const wizStep1 = (
      <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
          <label style={LABEL}>Name *</label>
          <HoloInput value={form.name} onChange={set('name')} placeholder="e.g. delete-old-snapshots" autoFocus />
        </div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
          <label style={LABEL}>Description</label>
          <HoloInput value={form.description} onChange={set('description')} placeholder="Optional description" />
        </div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
          <label style={LABEL}>Format</label>
          <Select
            options={FORMATS.map(f => ({ value: f, label: f === '*' ? 'All formats' : f }))}
            value={form.format}
            onChange={v => setForm(f => ({ ...f, format: v, scopeRepository: '', scopePath: '' }))}
          />
        </div>
      </div>
    )

    const wizStep2 = (
      <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
            <label style={LABEL}>Not downloaded for (days)</label>
            <HoloInput type="number" min="1" value={form.lastDownloadedDays} onChange={set('lastDownloadedDays')} placeholder="e.g. 30" style={{ MozAppearance: 'textfield' } as CSSProperties} />
          </div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
            <label style={LABEL}>Artifact age (days)</label>
            <HoloInput type="number" min="1" value={form.artifactAgeDays} onChange={set('artifactAgeDays')} placeholder="e.g. 90" style={{ MozAppearance: 'textfield' } as CSSProperties} />
          </div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
            <label style={LABEL}>Path prefix</label>
            <HoloInput value={form.pathPrefix} onChange={set('pathPrefix')} placeholder="e.g. /releases/" />
          </div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
            <label style={LABEL}>Name glob</label>
            <HoloInput value={form.nameGlob} onChange={set('nameGlob')} placeholder="e.g. *-SNAPSHOT*" />
          </div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 6, gridColumn: '1 / -1' }}>
            <label style={LABEL}>Retain N newest versions</label>
            <HoloInput type="number" min="0" value={form.retainNVersions} onChange={set('retainNVersions')} placeholder="e.g. 3 (0 = disabled)" style={{ MozAppearance: 'textfield' } as CSSProperties} />
            <span style={{ fontSize: 11, color: 'rgba(229,231,235,0.35)' }}>Keep the N most recent versions of each artifact even if they match other criteria.</span>
          </div>
        </div>
        {scopeSection}
      </div>
    )

    const wizStep3 = (
      <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
          <label style={LABEL}>Schedule (cron)</label>
          <HoloInput value={form.scheduleCron} onChange={set('scheduleCron')} placeholder="e.g. 0 2 * * * (default: every 6 hours)" />
          <span style={{ fontSize: 11, color: 'rgba(229,231,235,0.35)' }}>Leave blank to use the global default. Format: minute hour day month weekday</span>
        </div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
          <label style={LABEL}>Options</label>
          <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13, color: 'var(--holo-text-dim)', cursor: 'pointer' }}>
            <input type="checkbox" checked={form.enabled} onChange={e => setForm(f => ({ ...f, enabled: e.target.checked }))} />
            Enabled
          </label>
          <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13, color: 'var(--holo-text-dim)', cursor: 'pointer' }}>
            <input type="checkbox" checked={form.dryRun} onChange={e => setForm(f => ({ ...f, dryRun: e.target.checked }))} />
            Dry run (no deletes)
          </label>
        </div>
      </div>
    )

    return (
      <>
        {showBrowser && form.scopeRepository && (
          <PathBrowserModal
            repoName={form.scopeRepository}
            current={form.scopePath}
            onSelect={path => setForm(f => ({ ...f, scopePath: path }))}
            onClose={() => setShowBrowser(false)}
          />
        )}
        <Wizard
          steps={[
            { label: 'Identity', content: wizStep1 },
            { label: 'Criteria', content: wizStep2 },
            { label: 'Schedule', content: wizStep3 },
          ]}
          onFinish={handleFinish}
          finishLabel="Create Policy"
          onValidateStep={validateStep}
          onClose={onClose}
          loading={wizardLoading}
          error={wizardError}
        />
      </>
    )
  }

  // ── Edit mode: flat modal ─────────────────────────────────────────────
  return (
    <>
      {showBrowser && form.scopeRepository && (
        <PathBrowserModal
          repoName={form.scopeRepository}
          current={form.scopePath}
          onSelect={path => setForm(f => ({ ...f, scopePath: path }))}
          onClose={() => setShowBrowser(false)}
        />
      )}
      <HoloModal open={true} onClose={onClose}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
          <h2 style={{ fontSize: 17, fontWeight: 700, color: 'var(--holo-text)', margin: 0 }}>
            Edit Policy
          </h2>
          <HoloButton onClick={onClose} style={{ padding: 4 }}><X size={15} /></HoloButton>
        </div>

        <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
          <label style={LABEL}>Name *</label>
          <HoloInput value={form.name} onChange={set('name')} placeholder="e.g. delete-old-snapshots" />
        </div>

        <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
          <label style={LABEL}>Description</label>
          <HoloInput value={form.description} onChange={set('description')} placeholder="Optional description" />
        </div>

        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
            <label style={LABEL}>Format</label>
            <Select
              options={FORMATS.map(f => ({ value: f, label: f === '*' ? 'All formats' : f }))}
              value={form.format}
              onChange={v => setForm(f => ({ ...f, format: v, scopeRepository: '', scopePath: '' }))}
            />
          </div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
            <label style={LABEL}>Options</label>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 8, paddingTop: 4 }}>
              <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13, color: 'var(--holo-text-dim)', cursor: 'pointer' }}>
                <input type="checkbox" checked={form.enabled}
                  onChange={e => setForm(f => ({ ...f, enabled: e.target.checked }))} />
                Enabled
              </label>
              <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13, color: 'var(--holo-text-dim)', cursor: 'pointer' }}>
                <input type="checkbox" checked={form.dryRun}
                  onChange={e => setForm(f => ({ ...f, dryRun: e.target.checked }))} />
                Dry run (no deletes)
              </label>
            </div>
          </div>
        </div>

        <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
          <label style={LABEL}>Schedule (cron)</label>
          <HoloInput value={form.scheduleCron} onChange={set('scheduleCron')}
            placeholder="e.g. 0 2 * * * (default: every 6 hours)" />
          <span style={{ fontSize: 11, color: 'rgba(229,231,235,0.35)' }}>
            Leave blank to use the global default schedule. Format: minute hour day month weekday
          </span>
        </div>

        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
            <label style={LABEL}>Not downloaded for (days)</label>
            <HoloInput type="number" min="1" value={form.lastDownloadedDays}
              onChange={set('lastDownloadedDays')} placeholder="e.g. 30" style={{ MozAppearance: 'textfield' } as CSSProperties} />
          </div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
            <label style={LABEL}>Artifact age (days)</label>
            <HoloInput type="number" min="1" value={form.artifactAgeDays}
              onChange={set('artifactAgeDays')} placeholder="e.g. 90" style={{ MozAppearance: 'textfield' } as CSSProperties} />
          </div>
        </div>

        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
            <label style={LABEL}>Path prefix</label>
            <HoloInput value={form.pathPrefix}
              onChange={set('pathPrefix')} placeholder="e.g. com/example/" />
          </div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
            <label style={LABEL}>Name glob</label>
            <HoloInput value={form.nameGlob}
              onChange={set('nameGlob')} placeholder="e.g. *.jar or *-SNAPSHOT*" />
          </div>
        </div>

        <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
          <label style={LABEL}>Retain N newest versions</label>
          <HoloInput type="number" min="0" value={form.retainNVersions} onChange={set('retainNVersions')} placeholder="0 = disabled" style={{ MozAppearance: 'textfield' } as CSSProperties} />
          <span style={{ fontSize: 11, color: 'rgba(229,231,235,0.35)' }}>Keep the N most recent versions of each artifact even if they match other criteria.</span>
        </div>

        {scopeSection}

        {err && <div role="alert" style={{ fontSize: 12, color: 'var(--holo-red)', display: 'flex', gap: 6, alignItems: 'center' }}><AlertCircle size={13} />{err}</div>}

        <div style={{ display: 'flex', gap: 10, justifyContent: 'flex-end' }}>
          <HoloButton onClick={onClose}>Cancel</HoloButton>
          <HoloButton variant="primary" onClick={handleSave} icon={<Check size={14} />}>Save changes</HoloButton>
        </div>
      </HoloModal>
    </>
  )
}

// ── Page ───────────────────────────────────────────────────────────────────────

export default function CleanupPage() {
  const qc = useQueryClient()
  const [modal, setModal] = useState<'create' | CleanupPolicy | null>(null)
  const [running, setRunning] = useState<string | null>(null)
  const [previewId, setPreviewId] = useState<string | null>(null)
  const [runResult, setRunResult] = useState<{ tone: 'ok' | 'warn' | 'err'; text: string } | null>(null)

  const { data: policies = [], isLoading, refetch } = useQuery<CleanupPolicy[]>({
    queryKey: ['cleanupPolicies'],
    queryFn: () => nexusApi.listCleanupPolicies().then(r => r.data),
  })

  const deleteMut = useMutation({
    mutationFn: (id: string) => nexusApi.deleteCleanupPolicy(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['cleanupPolicies'] }),
  })

  const handleRun = async (id: string) => {
    setRunning(id)
    setRunResult(null)
    try {
      // A single-policy run is synchronous and returns its outcome.
      const res = await nexusApi.runCleanupPolicy(id)
      const r = res.data as { deleted?: number; freedBytes?: number; dryRun?: boolean; skipped?: boolean; skippedReason?: string; aborted?: boolean } | undefined
      if (r?.skipped) {
        setRunResult({ tone: 'warn', text: `Skipped: ${r.skippedReason ?? 'nothing to do'}` })
      } else if (r) {
        const freedKb = Math.round((r.freedBytes ?? 0) / 1024)
        const verb = r.dryRun ? 'Would delete' : 'Deleted'
        const text = `${verb} ${r.deleted ?? 0} asset(s), ${freedKb} KB${r.dryRun ? ' (dry run)' : ''}`
        // An aborted run hit its lock time limit: the counts are partial and the
        // rest of the backlog waits for the next run.
        setRunResult(r.aborted
          ? { tone: 'warn', text: `${text} — stopped early at the run time limit, the rest is left for the next run` }
          : { tone: 'ok', text })
      } else {
        setRunResult({ tone: 'ok', text: 'Cleanup started' })
      }
      refetch()
    } catch (e) {
      setRunResult({ tone: 'err', text: apiErrorMessage(e, 'Run failed') })
    } finally {
      setRunning(null)
    }
  }

  const previewPolicy = previewId ? policies.find(p => p.id === previewId) : null

  return (
    <div style={{ padding: 24, display: 'flex', flexDirection: 'column', gap: 20 }}>
      {(modal !== null) && (
        <PolicyModal
          initial={modal === 'create' ? null : modal}
          onClose={() => setModal(null)}
          onSaved={() => qc.invalidateQueries({ queryKey: ['cleanupPolicies'] })}
        />
      )}

      {previewId && previewPolicy && (
        <PreviewModal
          policyId={previewId}
          policyName={previewPolicy.name}
          onClose={() => setPreviewId(null)}
          onRun={() => { handleRun(previewId); setPreviewId(null) }}
        />
      )}

      <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', flexWrap: 'wrap', gap: 12 }}>
        <div>
          <div className="holo-section-label" style={{ marginBottom: 4 }}>ADMINISTRATION / CLEANUP</div>
          <h1 style={{ fontSize: 20, fontWeight: 700, margin: '0 0 3px', letterSpacing: '-0.01em', lineHeight: 1.2, background: 'linear-gradient(110deg, #7c5cff, #22d3ee 60%)', WebkitBackgroundClip: 'text', WebkitTextFillColor: 'transparent', backgroundClip: 'text' as const }}>Cleanup Policies</h1>
          <p style={{ fontSize: 12, color: 'var(--holo-text-faint)', margin: 0, maxWidth: 560 }}>
            Automate deletion of old, unused artifacts by criteria. Attach each policy to one or more
            repositories under Repositories → repository settings — unattached policies do not delete anything.
          </p>
        </div>
        <div style={{ display: 'flex', gap: 10 }}>
          <HoloButton onClick={() => refetch()} aria-label="Refresh"><RefreshCw size={16} /></HoloButton>
          <HoloButton variant="primary" icon={<Plus size={15} />} onClick={() => setModal('create')}>New Policy</HoloButton>
        </div>
      </div>

      {runResult && (
        <div
          role="status"
          onClick={() => setRunResult(null)}
          style={{
            cursor: 'pointer',
            padding: '10px 14px',
            borderRadius: 8,
            fontSize: 13,
            border: `1px solid ${runResult.tone === 'err' ? 'rgba(239,68,68,0.4)' : runResult.tone === 'warn' ? 'rgba(234,179,8,0.4)' : 'rgba(34,211,153,0.4)'}`,
            background: runResult.tone === 'err' ? 'rgba(239,68,68,0.1)' : runResult.tone === 'warn' ? 'rgba(234,179,8,0.1)' : 'rgba(34,211,153,0.1)',
            color: runResult.tone === 'err' ? '#fca5a5' : runResult.tone === 'warn' ? '#fde68a' : '#6ee7b7',
          }}
        >
          {runResult.text}
        </div>
      )}

      {isLoading ? (
        <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: 12, color: 'var(--holo-text-faint)', fontSize: 14, paddingTop: 48 }}>Loading…</div>
      ) : policies.length === 0 ? (
        <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: 12, color: 'var(--holo-text-faint)', fontSize: 14, paddingTop: 48 }}>
          <Trash2 size={40} style={{ opacity: 0.3 }} />
          <p>No cleanup policies configured</p>
          <HoloButton variant="primary" icon={<Plus size={14} />} onClick={() => setModal('create')}>Create first policy</HoloButton>
        </div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
          {policies.map(p => {
            const color = FORMAT_COLOR[p.format] ?? '#6b7280'
            const criteria = [
              p.criteria?.lastDownloadedDays && `≥${p.criteria.lastDownloadedDays}d not downloaded`,
              p.criteria?.artifactAgeDays && `age >${p.criteria.artifactAgeDays}d`,
              p.criteria?.pathPrefix && `path: ${p.criteria.pathPrefix}`,
              p.criteria?.nameGlob && `glob: ${p.criteria.nameGlob}`,
              p.retainNVersions && p.retainNVersions > 0 && `retain ≥${p.retainNVersions}`,
            ].filter(Boolean) as string[]

            return (
              <div
                key={p.id}
                style={{
                  display: 'grid',
                  gridTemplateColumns: '8px 88px 1fr auto auto',
                  alignItems: 'center',
                  gap: 14,
                  padding: '11px 16px',
                  background: 'rgba(10,8,28,0.97)',
                  border: '1px solid rgba(124,92,255,0.2)',
                  borderRadius: 10,
                  opacity: p.enabled ? 1 : 0.55,
                  transition: 'border-color 0.15s, background 0.15s',
                }}
                onMouseEnter={e => { (e.currentTarget as HTMLDivElement).style.borderColor = 'rgba(124,92,255,0.45)'; (e.currentTarget as HTMLDivElement).style.background = 'rgba(124,92,255,0.04)' }}
                onMouseLeave={e => { (e.currentTarget as HTMLDivElement).style.borderColor = 'rgba(124,92,255,0.2)'; (e.currentTarget as HTMLDivElement).style.background = 'rgba(10,8,28,0.97)' }}
              >
                {/* Status dot */}
                <span style={{
                  width: 7, height: 7, borderRadius: '50%', flexShrink: 0,
                  background: p.enabled ? 'var(--holo-green)' : 'rgba(255,255,255,0.2)',
                  boxShadow: p.enabled ? '0 0 5px var(--holo-green)' : 'none',
                  display: 'inline-block',
                }} />

                {/* Format badge */}
                <div style={{ display: 'flex', flexWrap: 'wrap', gap: 4 }}>
                  <span style={{
                    fontSize: 10, fontWeight: 600, padding: '2px 8px', borderRadius: 4,
                    textTransform: 'uppercase', letterSpacing: '0.3px', whiteSpace: 'nowrap',
                    background: color + '22', color,
                  }}>
                    {p.format === '*' ? 'all' : p.format}
                  </span>
                  {p.dryRun && <HoloPill tone="warn" style={{ fontSize: 10 }}>dry</HoloPill>}
                </div>

                {/* Name + meta */}
                <div style={{ minWidth: 0 }}>
                  <Truncated text={p.name} style={{ fontWeight: 600, fontSize: 13, color: 'var(--holo-text)' }} />
                  <div style={{ display: 'flex', gap: 6, marginTop: 3, flexWrap: 'wrap', alignItems: 'center' }}>
                    {criteria.length > 0 ? criteria.map(c => (
                      <span key={c} style={{ fontSize: 10, padding: '1px 6px', borderRadius: 4, background: 'rgba(124,92,255,0.1)', color: 'var(--holo-a)', fontFamily: 'monospace', whiteSpace: 'nowrap' }}>{c}</span>
                    )) : (
                      <span style={{ fontSize: 11, color: 'var(--holo-text-faint)' }}>{p.description || 'No criteria'}</span>
                    )}
                    {p.scope?.repositoryName && (
                      <span style={{
                        fontSize: 10, padding: '1px 6px', borderRadius: 4,
                        background: 'rgba(34,211,238,0.10)', color: 'var(--holo-b)',
                        border: '1px solid rgba(34,211,238,0.25)',
                        fontFamily: 'monospace', whiteSpace: 'nowrap',
                      }}>
                        {p.scope.repositoryName}{p.scope.pathPrefix ? ` / ${p.scope.pathPrefix}` : ''}
                      </span>
                    )}
                    {p.description && criteria.length > 0 && (
                      <Truncated as="span" text={p.description} style={{ fontSize: 11, color: 'var(--holo-text-faint)' }} />
                    )}
                  </div>
                </div>

                {/* Last run + schedule */}
                <div style={{ textAlign: 'right', fontSize: 11, color: 'var(--holo-text-faint)', display: 'flex', flexDirection: 'column', gap: 2, minWidth: 120 }}>
                  {p.lastRunAt && (
                    <span>{new Date(p.lastRunAt).toLocaleDateString()}{p.lastRunCount != null ? ` · ${p.lastRunCount} del` : ''}{p.lastRunFreedBytes != null ? ` · ${fmtBytes(p.lastRunFreedBytes)}` : ''}</span>
                  )}
                  <span style={{ color: p.scheduleCron ? 'var(--holo-a)' : 'var(--holo-text-faint)', fontFamily: 'monospace', fontSize: 10 }}>
                    {p.scheduleCron || 'default schedule'}
                  </span>
                </div>

                {/* Actions */}
                <div style={{ display: 'flex', gap: 6, justifyContent: 'flex-end' }}>
                  <HoloButton
                    style={{ background: 'rgba(34,211,238,0.08)', border: '1px solid rgba(34,211,238,0.22)', color: 'var(--holo-b)', padding: '4px 8px' }}
                    onClick={() => setPreviewId(p.id)}
                    title="Dry run preview"
                  >Preview</HoloButton>
                  <HoloButton
                    style={{ background: 'rgba(94,255,184,0.1)', border: '1px solid rgba(94,255,184,0.25)', color: 'var(--holo-green)', padding: '4px 8px' }}
                    icon={<Play size={12} />}
                    onClick={() => handleRun(p.id)}
                    disabled={running === p.id}
                    title="Run now"
                  >{running === p.id ? '…' : 'Run'}</HoloButton>
                  <HoloButton icon={<Pencil size={13} />} onClick={() => setModal(p)} title="Edit" style={{ padding: '4px 8px' }} />
                  <HoloButton variant="danger" icon={<Trash2 size={13} />} onClick={() => window.confirm(`Delete policy "${p.name}"?`) && deleteMut.mutate(p.id)} title="Delete" style={{ padding: '4px 8px' }} />
                </div>
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}
