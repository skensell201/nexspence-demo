import { useEffect, useRef, useState, lazy, Suspense } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Activity, Archive, ArrowRightLeft, ArrowUpCircle, CheckCircle, Database, Download, ExternalLink, GitBranch, HardDrive, Info, Network, Paperclip, Pause, Pencil, Play, Plus, RefreshCw, Share2, Shield, Trash2, Upload, Wifi, X } from 'lucide-react'
import { nexusApi, nexspenceApi, apiClient, apiErrorMessage, ImportRepoStats, ServiceStatus, RoutingRule, RoutingRuleInput, ReplicationRule, ReplicationHistory, ReplicationRuleInput, AuthConfig } from '@/api/client'
const MonitoringView = lazy(() => import('@/pages/MonitoringPage').then(m => ({ default: m.MonitoringView })))
import { Select } from '@/components/Select'
import { Truncated } from '@/components/Truncated'
import { HoloButton, HoloInput, HoloModal, HoloTabs, HoloCard, HoloTabItem, Wizard } from '@/components/holo'

interface BlobStore {
  id: string; name: string; type: string; usedBytes: number; quotaBytes?: number; config?: Record<string, unknown>
}
interface LinkedRepo { name: string; format: string; type: string; bytesUsed: number }
interface UsageResp {
  store: BlobStore
  linkedRepositories: LinkedRepo[]
  totalAssetBytes: number
  quotaRemaining?: number
  // group-specific fields
  members?: Array<{ id: string; name: string; usedBytes: number; quotaBytes?: number }>
  memberTotalUsed?: number
  memberTotalQuota?: number
}
interface SystemInfo { version: string; product: string }

type AdminTab = 'info' | 'blobs' | 'backup' | 'monitoring' | 'migration' | 'routing-rules' | 'replication' | 'saml' | 'promotion'
const VALID_TABS: AdminTab[] = ['info', 'blobs', 'backup', 'monitoring', 'migration', 'routing-rules', 'replication', 'saml', 'promotion']

function fmtGB(b: number) {
  return (b / 1024 / 1024 / 1024).toFixed(2) + ' GB'
}
function fmtMB(b: number) {
  return (b / 1024 / 1024).toFixed(1) + ' MB'
}
function fmtBytes(b: number) {
  if (b < 1024) return b + ' B'
  if (b < 1024 * 1024) return (b / 1024).toFixed(1) + ' KB'
  if (b < 1024 * 1024 * 1024) return fmtMB(b)
  return fmtGB(b)
}

function SamlTab() {
  const { data: authCfg, isLoading } = useQuery<AuthConfig>({
    queryKey: ['authConfig'],
    queryFn: () => nexusApi.getAuthConfig(),
    staleTime: 60_000,
  })
  const { data: services } = useQuery<ServiceStatus[]>({
    queryKey: ['systemServices'],
    queryFn: () => nexspenceApi.getServiceStatuses().then(r => r.data),
    staleTime: 30_000,
  })

  const samlSvc = services?.find(s => s.name.startsWith('SAML'))

  const row = (label: string, value?: string | null) => (
    <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', padding: '9px 0', borderBottom: '1px solid rgba(255,255,255,0.05)', fontSize: 13, gap: 16 }}>
      <span style={{ color: 'var(--holo-text-dim)', flexShrink: 0 }}>{label}</span>
      <span style={{ color: 'var(--holo-text)', fontWeight: 500, wordBreak: 'break-all', textAlign: 'right' as const }}>{value || '—'}</span>
    </div>
  )

  const statusDot = (status?: string) => {
    const color = status === 'ok' ? '#22c55e' : status === 'error' ? '#ef4444' : status === 'warn' ? '#f59e0b' : 'rgba(255,255,255,0.25)'
    const label = status === 'ok' ? 'Connected' : status === 'error' ? 'Error' : status === 'warn' ? 'Warning' : status === 'disabled' ? 'Disabled' : 'Unknown'
    return (
      <span style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 12, fontWeight: 600, color }}>
        <span style={{ width: 7, height: 7, borderRadius: '50%', background: color, boxShadow: status === 'ok' ? `0 0 5px ${color}66` : 'none', flexShrink: 0 }} />
        {label}
      </span>
    )
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      {/* SAML SP Configuration */}
      <HoloCard>
        <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--holo-text-dim)', textTransform: 'uppercase', letterSpacing: '0.06em', marginBottom: 16, display: 'flex', alignItems: 'center', gap: 8 }}>
          <Shield size={14} style={{ color: 'var(--holo-primary)' }} />
          SAML 2.0 Service Provider
          <span style={{ marginLeft: 'auto' }}>
            {isLoading ? null : authCfg?.samlEnabled ? (
              <span style={{ fontSize: 11, fontWeight: 700, padding: '2px 8px', borderRadius: 4, background: 'rgba(34,197,94,0.15)', color: '#22c55e' }}>ENABLED</span>
            ) : (
              <span style={{ fontSize: 11, fontWeight: 700, padding: '2px 8px', borderRadius: 4, background: 'rgba(255,255,255,0.07)', color: 'var(--holo-text-dim)' }}>DISABLED</span>
            )}
          </span>
        </div>

        {isLoading ? (
          <>
            <div className="holo-skeleton holo-skeleton--text" style={{ width: '80%' }} />
            <div className="holo-skeleton holo-skeleton--text" style={{ width: '65%', marginTop: 8 }} />
            <div className="holo-skeleton holo-skeleton--text" style={{ width: '75%', marginTop: 8 }} />
          </>
        ) : authCfg?.samlEnabled ? (
          <>
            {row('Display Name',   authCfg.samlDisplayName)}
            {row('SP Entity ID',   authCfg.samlEntityId)}
            {row('ACS URL',        authCfg.samlAcsUrl)}
            {row('IdP Metadata URL', authCfg.samlIdpMetadataUrl)}
            {row('Provisioning',   authCfg.samlProvisioning)}
            <div style={{ marginTop: 14 }}>
              <a
                href="/api/v1/auth/saml/metadata"
                target="_blank"
                rel="noreferrer"
                style={{ display: 'inline-flex', alignItems: 'center', gap: 6, fontSize: 12, fontWeight: 600, color: 'var(--holo-primary)', textDecoration: 'none', padding: '6px 12px', background: 'rgba(59,130,246,0.1)', borderRadius: 6, border: '1px solid rgba(59,130,246,0.2)' }}
              >
                <Download size={12} />
                Download SP Metadata XML
                <ExternalLink size={11} style={{ opacity: 0.7 }} />
              </a>
            </div>
          </>
        ) : (
          <div style={{ fontSize: 13, color: 'var(--holo-text-faint)', lineHeight: 1.6 }}>
            SAML SSO is not enabled. Set <code style={{ background: 'rgba(255,255,255,0.06)', padding: '1px 4px', borderRadius: 3 }}>saml.enabled: true</code> in <code style={{ background: 'rgba(255,255,255,0.06)', padding: '1px 4px', borderRadius: 3 }}>config.yaml</code> to activate.
          </div>
        )}
      </HoloCard>

      {/* SAML service health */}
      {samlSvc && (
        <HoloCard>
          <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--holo-text-dim)', textTransform: 'uppercase', letterSpacing: '0.06em', marginBottom: 12, display: 'flex', alignItems: 'center', gap: 8 }}>
            <Wifi size={14} /> SAML IdP Connection
            <span style={{ marginLeft: 'auto' }}>{statusDot(samlSvc.status)}</span>
          </div>
          <div style={{ fontSize: 12, color: 'var(--holo-text-dim)', lineHeight: 1.6, wordBreak: 'break-all' }}>{samlSvc.detail}</div>
        </HoloCard>
      )}

    </div>
  )
}

function RoutingRulesTab() {
  const qc = useQueryClient()
  const { data: rules = [], isLoading } = useQuery<RoutingRule[]>({
    queryKey: ['routing-rules'],
    queryFn: () => nexusApi.listRoutingRules().then(r => r.data),
  })

  const [modalOpen, setModalOpen] = useState(false)
  const [editing, setEditing] = useState<RoutingRule | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<RoutingRule | null>(null)
  const [form, setForm] = useState<{ name: string; description: string; mode: 'ALLOW' | 'BLOCK'; matchers: string[] }>({
    name: '', description: '', mode: 'ALLOW', matchers: [''],
  })
  const [saving, setSaving] = useState(false)
  const [err, setErr] = useState('')

  const openCreate = () => {
    setEditing(null)
    setForm({ name: '', description: '', mode: 'ALLOW', matchers: [''] })
    setErr('')
    setModalOpen(true)
  }

  const openEdit = (r: RoutingRule) => {
    setEditing(r)
    setForm({ name: r.name, description: r.description ?? '', mode: r.mode, matchers: r.matchers.length ? r.matchers : [''] })
    setErr('')
    setModalOpen(true)
  }

  const setMatcher = (i: number, v: string) =>
    setForm(f => { const m = [...f.matchers]; m[i] = v; return { ...f, matchers: m } })

  const addMatcher = () =>
    setForm(f => ({ ...f, matchers: [...f.matchers, ''] }))

  const removeMatcher = (i: number) =>
    setForm(f => ({ ...f, matchers: f.matchers.filter((_, idx) => idx !== i) }))

  const handleSave = async () => {
    setErr('')
    if (!form.name.trim()) { setErr('Name is required'); return }
    const matchers = form.matchers.filter(m => m.trim())
    const payload: RoutingRuleInput = {
      name: form.name.trim(),
      description: form.description.trim() || undefined,
      mode: form.mode,
      matchers,
    }
    setSaving(true)
    try {
      if (editing) {
        await nexusApi.updateRoutingRule(editing.id, payload)
      } else {
        await nexusApi.createRoutingRule(payload)
      }
      qc.invalidateQueries({ queryKey: ['routing-rules'] })
      setModalOpen(false)
    } catch (e) {
      setErr(apiErrorMessage(e, 'Save failed'))
    } finally {
      setSaving(false)
    }
  }

  const handleDelete = async () => {
    if (!deleteTarget) return
    try {
      await nexusApi.deleteRoutingRule(deleteTarget.id)
      qc.invalidateQueries({ queryKey: ['routing-rules'] })
    } finally {
      setDeleteTarget(null)
    }
  }

  const modeBadge = (mode: string) => (
    <span style={{
      fontSize: 11, fontWeight: 600, padding: '2px 8px', borderRadius: 4,
      background: mode === 'ALLOW' ? 'rgba(59,130,246,0.15)' : 'rgba(245,158,11,0.15)',
      color: mode === 'ALLOW' ? '#60a5fa' : '#fbbf24',
      border: `1px solid ${mode === 'ALLOW' ? 'rgba(59,130,246,0.3)' : 'rgba(245,158,11,0.3)'}`,
    }}>{mode}</span>
  )

  return (
    <HoloCard>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 16 }}>
        <span style={{ fontSize: 13, fontWeight: 600, color: 'var(--holo-text-dim)', textTransform: 'uppercase', letterSpacing: '0.06em' }}>
          Routing Rules
        </span>
        <HoloButton variant="primary" icon={<Plus size={13} />} onClick={openCreate}>
          Create Routing Rule
        </HoloButton>
      </div>

      {isLoading ? (
        <div className="holo-skeleton holo-skeleton--text" style={{ width: '60%' }} />
      ) : rules.length === 0 ? (
        <div style={{ color: 'var(--holo-text-faint)', fontSize: 13, textAlign: 'center', padding: '24px 0' }}>
          No routing rules configured
        </div>
      ) : (
        <table style={{ width: '100%', borderCollapse: 'collapse' as const, fontSize: 13 }}>
          <thead>
            <tr style={{ borderBottom: '1px solid rgba(255,255,255,0.08)' }}>
              {['Name', 'Mode', 'Matchers', 'Actions'].map(h => (
                <th key={h} style={{ textAlign: 'left' as const, padding: '6px 10px', color: 'var(--holo-text-dim)', fontWeight: 600, fontSize: 11, textTransform: 'uppercase' as const }}>{h}</th>
              ))}
            </tr>
          </thead>
          <tbody>
            {rules.map(r => (
              <tr key={r.id} style={{ borderBottom: '1px solid rgba(255,255,255,0.04)' }}>
                <td style={{ padding: '8px 10px', color: 'var(--holo-text)', fontWeight: 500 }}>{r.name}</td>
                <td style={{ padding: '8px 10px' }}>{modeBadge(r.mode)}</td>
                <td style={{ padding: '8px 10px', color: 'var(--holo-text-dim)', fontFamily: 'monospace', fontSize: 11 }}>
                  {r.matchers.length === 0 ? '—' : r.matchers.slice(0, 2).join(', ') + (r.matchers.length > 2 ? ` +${r.matchers.length - 2}` : '')}
                </td>
                <td style={{ padding: '8px 10px' }}>
                  <div style={{ display: 'flex', gap: 6 }}>
                    <HoloButton icon={<Pencil size={12} />} onClick={() => openEdit(r)}>Edit</HoloButton>
                    <HoloButton icon={<Trash2 size={12} />} onClick={() => setDeleteTarget(r)}>Delete</HoloButton>
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {modalOpen && (
        <ModalShell title={editing ? `Edit — ${editing.name}` : 'Create Routing Rule'} onClose={() => setModalOpen(false)} width={460}>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
            <div>
              <label style={{ fontSize: 11, fontWeight: 600, color: 'var(--holo-text-dim)', display: 'block', marginBottom: 5 }}>NAME *</label>
              <HoloInput value={form.name} onChange={e => setForm(f => ({ ...f, name: e.target.value }))} placeholder="block-snapshots" />
            </div>
            <div>
              <label style={{ fontSize: 11, fontWeight: 600, color: 'var(--holo-text-dim)', display: 'block', marginBottom: 5 }}>DESCRIPTION</label>
              <HoloInput value={form.description} onChange={e => setForm(f => ({ ...f, description: e.target.value }))} placeholder="Optional" />
            </div>
            <div>
              <label style={{ fontSize: 11, fontWeight: 600, color: 'var(--holo-text-dim)', display: 'block', marginBottom: 5 }}>MODE *</label>
              <Select
                value={form.mode}
                onChange={v => setForm(f => ({ ...f, mode: v as 'ALLOW' | 'BLOCK' }))}
                options={[
                  { value: 'ALLOW', label: 'ALLOW — only matching paths pass' },
                  { value: 'BLOCK', label: 'BLOCK — matching paths are skipped' },
                ]}
              />
            </div>
            <div>
              <label style={{ fontSize: 11, fontWeight: 600, color: 'var(--holo-text-dim)', display: 'block', marginBottom: 5 }}>MATCHERS (regex)</label>
              <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
                {form.matchers.map((m, i) => (
                  <div key={i} style={{ display: 'flex', gap: 6 }}>
                    <HoloInput
                      value={m}
                      onChange={e => setMatcher(i, e.target.value)}
                      placeholder=".*-SNAPSHOT.*"
                      style={{ flex: 1, fontFamily: 'monospace', fontSize: 12 }}
                    />
                    {form.matchers.length > 1 && (
                      <HoloButton icon={<X size={12} />} onClick={() => removeMatcher(i)} />
                    )}
                  </div>
                ))}
                <HoloButton icon={<Plus size={12} />} onClick={addMatcher} style={{ alignSelf: 'flex-start' }}>
                  Add matcher
                </HoloButton>
              </div>
            </div>
            {err && <div style={{ color: '#ef4444', fontSize: 12 }}>{err}</div>}
            <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8, marginTop: 4 }}>
              <HoloButton onClick={() => setModalOpen(false)}>Cancel</HoloButton>
              <HoloButton variant="primary" onClick={handleSave} disabled={saving}>
                {saving ? 'Saving…' : editing ? 'Save' : 'Create'}
              </HoloButton>
            </div>
          </div>
        </ModalShell>
      )}

      {deleteTarget && (
        <ModalShell title="Delete Routing Rule" onClose={() => setDeleteTarget(null)} width={380}>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
            <p style={{ margin: 0, fontSize: 13, color: 'var(--holo-text)' }}>
              Delete <strong>{deleteTarget.name}</strong>? Repositories using this rule will have it removed automatically.
            </p>
            <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8 }}>
              <HoloButton onClick={() => setDeleteTarget(null)}>Cancel</HoloButton>
              <HoloButton variant="danger" onClick={handleDelete}>Delete</HoloButton>
            </div>
          </div>
        </ModalShell>
      )}
    </HoloCard>
  )
}

function ReplicationTab() {
  const qc = useQueryClient()
  const { data: rules = [], isLoading } = useQuery<ReplicationRule[]>({
    queryKey: ['replication-rules'],
    queryFn: () => nexspenceApi.listReplicationRules().then(r => r.data ?? []),
  })

  const [modalOpen, setModalOpen] = useState(false)
  const [editing, setEditing] = useState<ReplicationRule | null>(null)
  const [expandedId, setExpandedId] = useState<string | null>(null)
  // Keyed by rule id, not a single shared array: a history request that resolves
  // after the user has closed one rule and opened another used to overwrite the
  // shared array unconditionally, so the earlier rule's runs were rendered under
  // the newly expanded rule's header — misattributing a broken target's failures
  // to a healthy one, or the reverse.
  const [history, setHistory] = useState<Record<string, ReplicationHistory[]>>({})
  const [histLoadingId, setHistLoadingId] = useState<string | null>(null)
  const [testResult, setTestResult] = useState<Record<string, string>>({})

  const [form, setForm] = useState<ReplicationRuleInput>({
    name: '', source_repo: '', target_url: '', target_repo: '',
    target_username: '', target_password: '', cron_expr: '0 2 * * *', enabled: true,
  })

  const { data: repos = [] } = useQuery({
    queryKey: ['repos-list'],
    queryFn: () => nexusApi.listRepositories().then(r => r.data as Array<{ name: string }>),
  })

  const createMutation = useMutation({
    mutationFn: (data: ReplicationRuleInput) => nexspenceApi.createReplicationRule(data),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ['replication-rules'] }); setModalOpen(false) },
  })

  const updateMutation = useMutation({
    mutationFn: ({ id, data }: { id: string; data: ReplicationRuleInput }) =>
      nexspenceApi.updateReplicationRule(id, data),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ['replication-rules'] }); setModalOpen(false) },
  })

  const deleteMutation = useMutation({
    mutationFn: (id: string) => nexspenceApi.deleteReplicationRule(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['replication-rules'] }),
  })

  const runMutation = useMutation({
    mutationFn: (id: string) => nexspenceApi.runReplicationRule(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['replication-rules'] }),
  })

  function openCreate() {
    setEditing(null)
    setForm({ name: '', source_repo: '', target_url: '', target_repo: '', target_username: '', target_password: '', cron_expr: '0 2 * * *', enabled: true })
    setModalOpen(true)
  }

  function openEdit(rule: ReplicationRule) {
    setEditing(rule)
    setForm({
      name: rule.name, source_repo: rule.source_repo, target_url: rule.target_url,
      target_repo: rule.target_repo, target_username: rule.target_username,
      target_password: '', cron_expr: rule.cron_expr, enabled: rule.enabled,
    })
    setModalOpen(true)
  }

  async function toggleHistory(rule: ReplicationRule) {
    if (expandedId === rule.id) { setExpandedId(null); return }
    setExpandedId(rule.id)
    setHistLoadingId(rule.id)
    try {
      const r = await nexspenceApi.listReplicationHistory(rule.id)
      // Stored under the rule this request was made for, whatever is expanded
      // by the time it resolves.
      setHistory(prev => ({ ...prev, [rule.id]: r.data ?? [] }))
    } finally {
      setHistLoadingId(cur => (cur === rule.id ? null : cur))
    }
  }

  async function testConn(id: string) {
    setTestResult(prev => ({ ...prev, [id]: 'testing…' }))
    try {
      await nexspenceApi.testReplicationRule(id)
      setTestResult(prev => ({ ...prev, [id]: '✓ Connected' }))
    } catch (e: unknown) {
      const msg = (e as { response?: { data?: { error?: string } } })?.response?.data?.error || 'Failed'
      setTestResult(prev => ({ ...prev, [id]: `✗ ${msg}` }))
    }
    setTimeout(() => setTestResult(prev => { const n = { ...prev }; delete n[id]; return n }), 5000)
  }

  function handleSubmit() {
    if (editing) {
      updateMutation.mutate({ id: editing.id, data: form })
    } else {
      createMutation.mutate(form)
    }
  }

  const fmtDate = (s: string | null) => s ? new Date(s).toLocaleString() : '—'
  const fmtDur = (ms: number) => ms < 1000 ? `${ms}ms` : `${(ms / 1000).toFixed(1)}s`

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
        <span style={{ color: '#94a3b8', fontSize: 13 }}>
          Push artifacts from local repositories to remote Nexspence instances on a cron schedule.
        </span>
        <HoloButton onClick={openCreate}>
          <Plus size={13} style={{ marginRight: 5 }} /> New Rule
        </HoloButton>
      </div>

      {isLoading && <p style={{ color: '#64748b' }}>Loading…</p>}

      {!isLoading && rules.length === 0 && (
        <p style={{ color: '#64748b', fontSize: 13 }}>No replication rules configured.</p>
      )}

      {rules.map(rule => (
        <HoloCard key={rule.id} style={{ marginBottom: 10 }}>
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start' }}>
            <div>
              <div style={{ fontWeight: 600, color: '#e2e8f0', marginBottom: 4 }}>{rule.name}</div>
              <div style={{ fontSize: 12, color: '#64748b' }}>
                <span style={{ color: '#94a3b8' }}>{rule.source_repo}</span>
                {' → '}
                <span style={{ color: '#94a3b8' }}>{rule.target_url}/{rule.target_repo}</span>
              </div>
              <div style={{ fontSize: 11, color: '#475569', marginTop: 4 }}>
                <span>cron: {rule.cron_expr}</span>
                {' · '}
                <span style={{ color: rule.enabled ? '#22c55e' : '#ef4444' }}>
                  {rule.enabled ? 'enabled' : 'disabled'}
                </span>
                {rule.last_run_at && (
                  <>
                    {' · last run: '}
                    <span style={{ color: rule.last_run_status === 'ok' ? '#22c55e' : rule.last_run_status === 'error' ? '#ef4444' : '#f59e0b' }}>
                      {rule.last_run_status}
                    </span>
                    {' '}{fmtDate(rule.last_run_at)}
                  </>
                )}
                {testResult[rule.id] && (
                  <span style={{ marginLeft: 8, color: testResult[rule.id].startsWith('✓') ? '#22c55e' : '#ef4444' }}>
                    {testResult[rule.id]}
                  </span>
                )}
              </div>
            </div>
            <div style={{ display: 'flex', gap: 6 }}>
              <HoloButton onClick={() => testConn(rule.id)} title="Test connection">
                <Wifi size={12} />
              </HoloButton>
              <HoloButton onClick={() => runMutation.mutate(rule.id)} title="Run now">
                <Play size={12} />
              </HoloButton>
              <HoloButton onClick={() => toggleHistory(rule)} title="History">
                <Activity size={12} />
              </HoloButton>
              <HoloButton onClick={() => openEdit(rule)} title="Edit">
                <Pencil size={12} />
              </HoloButton>
              <HoloButton onClick={() => deleteMutation.mutate(rule.id)} title="Delete">
                <Trash2 size={12} />
              </HoloButton>
            </div>
          </div>

          {expandedId === rule.id && (
            <div style={{ marginTop: 12, borderTop: '1px solid rgba(255,255,255,0.06)', paddingTop: 10 }}>
              {histLoadingId === rule.id && <p style={{ color: '#64748b', fontSize: 12 }}>Loading history…</p>}
              {histLoadingId !== rule.id && (history[rule.id]?.length ?? 0) === 0 && (
                <p style={{ color: '#64748b', fontSize: 12 }}>No runs recorded yet.</p>
              )}
              {histLoadingId !== rule.id && (history[rule.id]?.length ?? 0) > 0 && (
                <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 12 }}>
                  <thead>
                    <tr style={{ color: '#64748b', textAlign: 'left' }}>
                      <th style={{ padding: '4px 8px' }}>Started</th>
                      <th style={{ padding: '4px 8px' }}>Duration</th>
                      <th style={{ padding: '4px 8px' }}>Pushed</th>
                      <th style={{ padding: '4px 8px' }}>Skipped</th>
                      <th style={{ padding: '4px 8px' }}>Failed</th>
                      <th style={{ padding: '4px 8px' }}>Bytes</th>
                      <th style={{ padding: '4px 8px' }}>Error</th>
                    </tr>
                  </thead>
                  <tbody>
                    {(history[rule.id] ?? []).map(h => (
                      <tr key={h.id} style={{ color: '#94a3b8', borderTop: '1px solid rgba(255,255,255,0.04)' }}>
                        <td style={{ padding: '4px 8px' }}>{fmtDate(h.started_at)}</td>
                        <td style={{ padding: '4px 8px' }}>{fmtDur(h.duration_ms)}</td>
                        <td style={{ padding: '4px 8px', color: '#22c55e' }}>{h.pushed_count}</td>
                        <td style={{ padding: '4px 8px' }}>{h.skipped_count}</td>
                        <td style={{ padding: '4px 8px', color: h.failed_count > 0 ? '#ef4444' : '#94a3b8' }}>{h.failed_count}</td>
                        <td style={{ padding: '4px 8px' }}>{fmtBytes(h.transferred_bytes)}</td>
                        <Truncated as="td" text={h.error || '—'} style={{ padding: '4px 8px', color: '#ef4444', maxWidth: 200 }} />
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
            </div>
          )}
        </HoloCard>
      ))}

      {modalOpen && (
        <ModalShell title={editing ? 'Edit Replication Rule' : 'New Replication Rule'} onClose={() => setModalOpen(false)} width={480}>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
            <div>
              <label style={{ fontSize: 11, fontWeight: 600, color: 'var(--holo-text-dim)', display: 'block', marginBottom: 5 }}>RULE NAME *</label>
              <HoloInput value={form.name} onChange={e => setForm(f => ({ ...f, name: e.target.value }))} placeholder="prod-mirror" />
            </div>
            <div>
              <label style={{ fontSize: 11, fontWeight: 600, color: 'var(--holo-text-dim)', display: 'block', marginBottom: 5 }}>SOURCE REPOSITORY *</label>
              <Select
                value={form.source_repo}
                onChange={v => setForm(f => ({ ...f, source_repo: v }))}
                options={repos.map((r: { name: string }) => ({ value: r.name, label: r.name }))}
                placeholder="Select repository…"
              />
            </div>
            <div>
              <label style={{ fontSize: 11, fontWeight: 600, color: 'var(--holo-text-dim)', display: 'block', marginBottom: 5 }}>TARGET URL *</label>
              <HoloInput value={form.target_url} onChange={e => setForm(f => ({ ...f, target_url: e.target.value }))} placeholder="https://nexspence.example.com" />
            </div>
            <div>
              <label style={{ fontSize: 11, fontWeight: 600, color: 'var(--holo-text-dim)', display: 'block', marginBottom: 5 }}>TARGET REPOSITORY *</label>
              <HoloInput value={form.target_repo} onChange={e => setForm(f => ({ ...f, target_repo: e.target.value }))} placeholder="my-repo-mirror" />
            </div>
            <div>
              <label style={{ fontSize: 11, fontWeight: 600, color: 'var(--holo-text-dim)', display: 'block', marginBottom: 5 }}>TARGET USERNAME</label>
              <HoloInput value={form.target_username} onChange={e => setForm(f => ({ ...f, target_username: e.target.value }))} placeholder="admin" />
            </div>
            <div>
              <label style={{ fontSize: 11, fontWeight: 600, color: 'var(--holo-text-dim)', display: 'block', marginBottom: 5 }}>
                {editing ? 'TARGET PASSWORD (leave blank to keep existing)' : 'TARGET PASSWORD'}
              </label>
              <HoloInput type="password" value={form.target_password} onChange={e => setForm(f => ({ ...f, target_password: e.target.value }))} />
            </div>
            <div>
              <label style={{ fontSize: 11, fontWeight: 600, color: 'var(--holo-text-dim)', display: 'block', marginBottom: 5 }}>CRON EXPRESSION</label>
              <HoloInput value={form.cron_expr} onChange={e => setForm(f => ({ ...f, cron_expr: e.target.value }))} placeholder="0 2 * * *" style={{ fontFamily: 'monospace' }} />
            </div>
            <label style={{ fontSize: 12, color: '#94a3b8', display: 'flex', alignItems: 'center', gap: 8 }}>
              <input type="checkbox" checked={form.enabled} onChange={e => setForm(f => ({ ...f, enabled: e.target.checked }))} />
              Enabled
            </label>
            <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8, marginTop: 4 }}>
              <HoloButton onClick={() => setModalOpen(false)}>Cancel</HoloButton>
              <HoloButton variant="primary" onClick={handleSubmit} disabled={createMutation.isPending || updateMutation.isPending}>
                {editing ? 'Save' : 'Create'}
              </HoloButton>
            </div>
          </div>
        </ModalShell>
      )}
    </div>
  )
}

// ── Promotion interfaces ──────────────────────────────────────────

interface PromotionRule {
  id: string
  name: string
  from_repo: string
  to_repo: string
  path_filter?: string
  require_scan_pass: boolean
  require_manual_approval: boolean
  created_at: string
}

interface PromotionRequest {
  id: string
  rule_id: string
  component_id: string
  status: 'pending' | 'approved' | 'rejected' | 'completed' | 'failed'
  requested_by: string
  reviewed_by?: string
  completed_at?: string
  error?: string
  created_at: string
}

// ── PromotionRuleModal ────────────────────────────────────────────

function PromotionRuleModal({
  open,
  rule,
  repos,
  onClose,
  onSaved,
}: {
  open: boolean
  rule: PromotionRule | null
  repos: Array<{ name: string }>
  onClose: () => void
  onSaved: () => void
}) {
  const [name, setName] = useState('')
  const [fromRepo, setFromRepo] = useState('')
  const [toRepo, setToRepo] = useState('')
  const [pathFilter, setPathFilter] = useState('')
  const [requireScanPass, setRequireScanPass] = useState(false)
  const [requireManualApproval, setRequireManualApproval] = useState(false)
  const [saving, setSaving] = useState(false)
  const [err, setErr] = useState('')

  // Reset form when rule or open changes
  useEffect(() => {
    if (open) {
      setName(rule?.name ?? '')
      setFromRepo(rule?.from_repo ?? '')
      setToRepo(rule?.to_repo ?? '')
      setPathFilter(rule?.path_filter ?? '')
      setRequireScanPass(rule?.require_scan_pass ?? false)
      setRequireManualApproval(rule?.require_manual_approval ?? false)
      setErr('')
    }
  }, [open, rule])

  if (!open) return null

  const handleSave = async () => {
    setErr('')
    if (!name.trim()) { setErr('Name is required'); return }
    if (!fromRepo) { setErr('From repository is required'); return }
    if (!toRepo) { setErr('To repository is required'); return }
    const payload = {
      name: name.trim(),
      from_repo: fromRepo,
      to_repo: toRepo,
      path_filter: pathFilter.trim() || undefined,
      require_scan_pass: requireScanPass,
      require_manual_approval: requireManualApproval,
    }
    setSaving(true)
    try {
      if (rule) {
        await apiClient.put(`/api/v1/promotion/rules/${rule.id}`, payload)
      } else {
        await apiClient.post('/api/v1/promotion/rules', payload)
      }
      onSaved()
      onClose()
    } catch (e: unknown) {
      const msg = (e as { response?: { data?: { error?: string } } })?.response?.data?.error ?? 'Save failed'
      setErr(msg)
    } finally {
      setSaving(false)
    }
  }

  return (
    <ModalShell title={rule ? `Edit — ${rule.name}` : 'Create Promotion Rule'} onClose={onClose} width={460}>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
        <div>
          <label style={{ fontSize: 11, fontWeight: 600, color: 'var(--holo-text-dim)', display: 'block', marginBottom: 5 }}>NAME *</label>
          <HoloInput value={name} onChange={e => setName(e.target.value)} placeholder="promote-to-release" />
        </div>
        <div>
          <label style={{ fontSize: 11, fontWeight: 600, color: 'var(--holo-text-dim)', display: 'block', marginBottom: 5 }}>FROM REPOSITORY *</label>
          <Select
            value={fromRepo}
            onChange={setFromRepo}
            options={repos.map(r => ({ value: r.name, label: r.name }))}
            placeholder="Select source repository…"
          />
        </div>
        <div>
          <label style={{ fontSize: 11, fontWeight: 600, color: 'var(--holo-text-dim)', display: 'block', marginBottom: 5 }}>TO REPOSITORY *</label>
          <Select
            value={toRepo}
            onChange={setToRepo}
            options={repos.map(r => ({ value: r.name, label: r.name }))}
            placeholder="Select target repository…"
          />
        </div>
        <div>
          <label style={{ fontSize: 11, fontWeight: 600, color: 'var(--holo-text-dim)', display: 'block', marginBottom: 5 }}>PATH FILTER (CEL)</label>
          <HoloInput
            value={pathFilter}
            onChange={e => setPathFilter(e.target.value)}
            placeholder='path.startsWith("/com/example/")'
            style={{ fontFamily: 'monospace', fontSize: 12 }}
          />
        </div>
        <label style={{ fontSize: 12, color: '#94a3b8', display: 'flex', alignItems: 'center', gap: 8 }}>
          <input type="checkbox" checked={requireScanPass} onChange={e => setRequireScanPass(e.target.checked)} />
          Require scan pass (no HIGH/CRITICAL CVEs)
        </label>
        <label style={{ fontSize: 12, color: '#94a3b8', display: 'flex', alignItems: 'center', gap: 8 }}>
          <input type="checkbox" checked={requireManualApproval} onChange={e => setRequireManualApproval(e.target.checked)} />
          Require manual approval
        </label>
        {err && <div style={{ color: '#ef4444', fontSize: 12 }}>{err}</div>}
        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8, marginTop: 4 }}>
          <HoloButton onClick={onClose}>Cancel</HoloButton>
          <HoloButton variant="primary" onClick={handleSave} disabled={saving}>
            {saving ? 'Saving…' : rule ? 'Save' : 'Create'}
          </HoloButton>
        </div>
      </div>
    </ModalShell>
  )
}

// ── PromotionTab ──────────────────────────────────────────────────

function PromotionTab() {
  const qc = useQueryClient()
  const [statusFilter, setStatusFilter] = useState('all')
  const [ruleModalOpen, setRuleModalOpen] = useState(false)
  const [editingRule, setEditingRule] = useState<PromotionRule | null>(null)
  const [deleteRuleTarget, setDeleteRuleTarget] = useState<PromotionRule | null>(null)
  const [rejectTarget, setRejectTarget] = useState<PromotionRequest | null>(null)
  const [rejectReason, setRejectReason] = useState('')

  const { data: rules = [], isLoading: rulesLoading } = useQuery<PromotionRule[]>({
    queryKey: ['promotion-rules'],
    queryFn: () => apiClient.get('/api/v1/promotion/rules').then(r => r.data),
  })

  const { data: requests = [], isLoading: requestsLoading } = useQuery<PromotionRequest[]>({
    queryKey: ['promotion-requests', statusFilter],
    queryFn: () => {
      const params = statusFilter !== 'all' ? { status: statusFilter } : {}
      return apiClient.get('/api/v1/promotion/requests', { params }).then(r => r.data)
    },
    refetchInterval: 10_000,
  })

  const { data: repos = [] } = useQuery({
    queryKey: ['repos-list'],
    queryFn: () => nexusApi.listRepositories().then(r => r.data as Array<{ name: string }>),
  })

  const approveMutation = useMutation({
    mutationFn: (req: PromotionRequest) =>
      apiClient.post(`/api/v1/promotion/requests/${req.id}/approve`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['promotion-requests'] }),
  })

  const rejectMutation = useMutation({
    mutationFn: ({ req, reason }: { req: PromotionRequest; reason: string }) =>
      apiClient.post(`/api/v1/promotion/requests/${req.id}/reject`, { reason }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['promotion-requests'] })
      setRejectTarget(null)
      setRejectReason('')
    },
  })

  const deleteRuleMutation = useMutation({
    mutationFn: (id: string) => apiClient.delete(`/api/v1/promotion/rules/${id}`),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['promotion-rules'] })
      setDeleteRuleTarget(null)
    },
  })

  const ruleNameById = (id: string) => rules.find(r => r.id === id)?.name ?? id.slice(0, 8)

  const statusColor = (s: PromotionRequest['status']) => {
    const map: Record<string, string> = {
      pending:   '#f59e0b',
      approved:  '#3b82f6',
      rejected:  '#ef4444',
      completed: '#22c55e',
      failed:    '#ef4444',
    }
    return map[s] ?? '#94a3b8'
  }

  const openCreateRule = () => { setEditingRule(null); setRuleModalOpen(true) }
  const openEditRule = (r: PromotionRule) => { setEditingRule(r); setRuleModalOpen(true) }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>

      {/* Rules section */}
      <HoloCard>
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 16 }}>
          <span style={{ fontSize: 13, fontWeight: 600, color: 'var(--holo-text-dim)', textTransform: 'uppercase', letterSpacing: '0.06em' }}>
            Promotion Rules
          </span>
          <HoloButton variant="primary" icon={<Plus size={13} />} onClick={openCreateRule}>
            Create Rule
          </HoloButton>
        </div>

        {rulesLoading ? (
          <div className="holo-skeleton holo-skeleton--text" style={{ width: '60%' }} />
        ) : rules.length === 0 ? (
          <div style={{ color: 'var(--holo-text-faint)', fontSize: 13, textAlign: 'center', padding: '24px 0' }}>
            No promotion rules configured
          </div>
        ) : (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
            {rules.map(rule => (
              <div key={rule.id} style={{ padding: '12px 14px', background: 'rgba(255,255,255,0.03)', borderRadius: 10, border: '1px solid rgba(255,255,255,0.07)' }}>
                <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', gap: 12 }}>
                  <div style={{ minWidth: 0 }}>
                    <div style={{ fontWeight: 600, color: 'var(--holo-text)', marginBottom: 4 }}>{rule.name}</div>
                    <div style={{ fontSize: 12, color: '#94a3b8', display: 'flex', alignItems: 'center', gap: 6 }}>
                      <span style={{ color: 'var(--holo-text-dim)' }}>{rule.from_repo}</span>
                      <ArrowUpCircle size={11} style={{ color: 'var(--holo-primary)', flexShrink: 0 }} />
                      <span style={{ color: 'var(--holo-text-dim)' }}>{rule.to_repo}</span>
                    </div>
                    {rule.path_filter && (
                      <div style={{ fontSize: 11, fontFamily: 'monospace', color: '#94a3b8', marginTop: 4, background: 'rgba(255,255,255,0.04)', padding: '2px 6px', borderRadius: 4, display: 'inline-block' }}>
                        {rule.path_filter}
                      </div>
                    )}
                    <div style={{ display: 'flex', gap: 6, marginTop: 6 }}>
                      {rule.require_scan_pass && (
                        <span style={{ fontSize: 10, fontWeight: 600, padding: '2px 7px', borderRadius: 4, background: 'rgba(34,197,94,0.12)', color: '#22c55e', border: '1px solid rgba(34,197,94,0.25)' }}>
                          Scan Pass
                        </span>
                      )}
                      {rule.require_manual_approval && (
                        <span style={{ fontSize: 10, fontWeight: 600, padding: '2px 7px', borderRadius: 4, background: 'rgba(245,158,11,0.12)', color: '#f59e0b', border: '1px solid rgba(245,158,11,0.25)' }}>
                          Manual Approval
                        </span>
                      )}
                    </div>
                  </div>
                  <div style={{ display: 'flex', gap: 6, flexShrink: 0 }}>
                    <HoloButton icon={<Pencil size={12} />} onClick={() => openEditRule(rule)}>Edit</HoloButton>
                    <HoloButton icon={<Trash2 size={12} />} onClick={() => setDeleteRuleTarget(rule)}>Delete</HoloButton>
                  </div>
                </div>
              </div>
            ))}
          </div>
        )}
      </HoloCard>

      {/* Requests section */}
      <HoloCard>
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 16 }}>
          <span style={{ fontSize: 13, fontWeight: 600, color: 'var(--holo-text-dim)', textTransform: 'uppercase', letterSpacing: '0.06em' }}>
            Promotion Requests
          </span>
          <Select
            value={statusFilter}
            onChange={setStatusFilter}
            options={[
              { value: 'all',       label: 'All' },
              { value: 'pending',   label: 'Pending' },
              { value: 'completed', label: 'Completed' },
              { value: 'rejected',  label: 'Rejected' },
              { value: 'failed',    label: 'Failed' },
            ]}
            style={{ width: 140 }}
          />
        </div>

        {requestsLoading ? (
          <div className="holo-skeleton holo-skeleton--text" style={{ width: '60%' }} />
        ) : requests.length === 0 ? (
          <div style={{ color: 'var(--holo-text-faint)', fontSize: 13, textAlign: 'center', padding: '24px 0' }}>
            No promotion requests
          </div>
        ) : (
          <table style={{ width: '100%', borderCollapse: 'collapse' as const, fontSize: 13 }}>
            <thead>
              <tr style={{ borderBottom: '1px solid rgba(255,255,255,0.08)' }}>
                {['Component', 'Rule', 'Status', 'Requested', 'Actions'].map(h => (
                  <th key={h} style={{ textAlign: 'left' as const, padding: '6px 10px', color: 'var(--holo-text-dim)', fontWeight: 600, fontSize: 11, textTransform: 'uppercase' as const }}>{h}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {requests.map(req => (
                <tr key={req.id} style={{ borderBottom: '1px solid rgba(255,255,255,0.04)' }}>
                  <td style={{ padding: '8px 10px', fontFamily: 'monospace', fontSize: 11, color: 'var(--holo-text)' }}>
                    {req.component_id.slice(0, 8)}&hellip;
                  </td>
                  <td style={{ padding: '8px 10px', color: 'var(--holo-text-dim)' }}>
                    {ruleNameById(req.rule_id)}
                  </td>
                  <td style={{ padding: '8px 10px' }}>
                    <span style={{ fontWeight: 600, color: statusColor(req.status) }}>{req.status}</span>
                  </td>
                  <td style={{ padding: '8px 10px', color: 'var(--holo-text-faint)', fontSize: 11 }}>
                    {new Date(req.created_at).toLocaleString()}
                  </td>
                  <td style={{ padding: '8px 10px' }}>
                    {req.status === 'pending' && (
                      <div style={{ display: 'flex', gap: 6 }}>
                        <HoloButton
                          variant="primary"
                          icon={<CheckCircle size={12} />}
                          onClick={() => approveMutation.mutate(req)}
                          disabled={approveMutation.isPending}
                        >
                          Approve
                        </HoloButton>
                        <HoloButton
                          icon={<X size={12} />}
                          onClick={() => { setRejectTarget(req); setRejectReason('') }}
                        >
                          Reject
                        </HoloButton>
                      </div>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </HoloCard>

      {/* Rule modal */}
      <PromotionRuleModal
        open={ruleModalOpen}
        rule={editingRule}
        repos={repos}
        onClose={() => setRuleModalOpen(false)}
        onSaved={() => qc.invalidateQueries({ queryKey: ['promotion-rules'] })}
      />

      {/* Delete rule confirm */}
      {deleteRuleTarget && (
        <ModalShell title="Delete Promotion Rule" onClose={() => setDeleteRuleTarget(null)} width={380}>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
            <p style={{ margin: 0, fontSize: 13, color: 'var(--holo-text)' }}>
              Delete rule <strong>{deleteRuleTarget.name}</strong>? Pending requests using this rule will not be processed.
            </p>
            <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8 }}>
              <HoloButton onClick={() => setDeleteRuleTarget(null)}>Cancel</HoloButton>
              <HoloButton variant="danger" onClick={() => deleteRuleMutation.mutate(deleteRuleTarget.id)} disabled={deleteRuleMutation.isPending}>
                Delete
              </HoloButton>
            </div>
          </div>
        </ModalShell>
      )}

      {/* Reject modal */}
      {rejectTarget && (
        <ModalShell title="Reject Promotion Request" onClose={() => setRejectTarget(null)} width={400}>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
            <div>
              <label style={{ fontSize: 11, fontWeight: 600, color: 'var(--holo-text-dim)', display: 'block', marginBottom: 5 }}>REASON</label>
              <HoloInput
                value={rejectReason}
                onChange={e => setRejectReason(e.target.value)}
                placeholder="Optional reason for rejection"
              />
            </div>
            <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8 }}>
              <HoloButton onClick={() => setRejectTarget(null)}>Cancel</HoloButton>
              <HoloButton variant="danger" onClick={() => rejectMutation.mutate({ req: rejectTarget, reason: rejectReason })} disabled={rejectMutation.isPending}>
                Reject
              </HoloButton>
            </div>
          </div>
        </ModalShell>
      )}
    </div>
  )
}

export default function AdminPage() {
  const [searchParams, setSearchParams] = useSearchParams()
  const tabParam = searchParams.get('tab') as AdminTab | null
  const tab: AdminTab = tabParam && VALID_TABS.includes(tabParam) ? tabParam : 'info'
  const setTab = (t: AdminTab) => {
    const next = new URLSearchParams(searchParams)
    next.set('tab', t)
    setSearchParams(next, { replace: true })
  }

  const [exportBusy, setExportBusy] = useState(false)
  const [importFile, setImportFile]             = useState<File | null>(null)
  const [importTargetName, setImportTargetName] = useState('')
  const [importConflict, setImportConflict]     = useState('skip')
  const [importBusy, setImportBusy]             = useState(false)
  const [importResult, setImportResult]         = useState<{ imported: ImportRepoStats } | null>(null)
  const [importError, setImportError]           = useState<string | null>(null)
  const [restoreBusy, setRestoreBusy] = useState(false)
  const [restoreResult, setRestoreResult] = useState<Record<string, number> | null>(null)
  const [restoreError, setRestoreError] = useState('')
  const fileInputRef = useRef<HTMLInputElement>(null)
  const importFileRef = useRef<HTMLInputElement>(null)
  const [editingQuota, setEditingQuota] = useState<string | null>(null) // blob store id
  const [quotaInput, setQuotaInput] = useState('')
  const [detailName, setDetailName] = useState<string | null>(null) // open detail modal for this blob store
  const [createOpen, setCreateOpen] = useState(false)
  const qc = useQueryClient()

  const handleExport = async () => {
    setExportBusy(true)
    try {
      const res = await nexspenceApi.exportBackup()
      const url = URL.createObjectURL(res.data as Blob)
      const a = document.createElement('a')
      const ts = new Date().toISOString().replace(/[:.]/g, '-').slice(0, 19)
      a.href = url
      a.download = `nexspence-backup-${ts}.tar.gz`
      a.click()
      URL.revokeObjectURL(url)
    } finally {
      setExportBusy(false)
    }
  }

  const handleImportRepo = async () => {
    if (!importFile) return
    setImportBusy(true)
    setImportResult(null)
    setImportError(null)
    try {
      const res = await nexspenceApi.importRepo(importFile, importTargetName, importConflict)
      setImportResult(res.data)
    } catch (e) {
      setImportError(apiErrorMessage(e, 'Import failed'))
    } finally {
      setImportBusy(false)
    }
  }

  const handleRestore = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    if (!file) return
    setRestoreResult(null)
    setRestoreError('')
    setRestoreBusy(true)
    try {
      const res = await nexspenceApi.restoreBackup(file)
      setRestoreResult(res.data.restored)
    } catch (err: unknown) {
      const msg = (err as { response?: { data?: { error?: string } } })?.response?.data?.error ?? 'Restore failed'
      setRestoreError(msg)
    } finally {
      setRestoreBusy(false)
      if (fileInputRef.current) fileInputRef.current.value = ''
    }
  }

  const { data: status, isLoading: statusLoading, refetch: refetchStatus } = useQuery({
    queryKey: ['status'],
    queryFn: () => nexusApi.getStatus().then(r => r.data),
  })

  const { data: blobs = [], isLoading: blobsLoading, refetch: refetchBlobs } = useQuery<BlobStore[]>({
    queryKey: ['blobstores'],
    queryFn: () => nexusApi.listBlobStores().then(r => r.data),
  })

  const { data: info } = useQuery<SystemInfo>({
    queryKey: ['systemInfo'],
    queryFn: () => nexspenceApi.getSystemInfo().then(r => r.data),
  })

  const { data: services, isFetching: servicesFetching, refetch: refetchServices } = useQuery<ServiceStatus[]>({
    queryKey: ['systemServices'],
    queryFn: () => nexspenceApi.getServiceStatuses().then(r => r.data),
    staleTime: 30_000,
  })

  const quotaMut = useMutation({
    mutationFn: ({ bs, gb }: { bs: BlobStore; gb: string }) => {
      const bytes = gb.trim() === '' ? null : Math.round(parseFloat(gb) * 1024 * 1024 * 1024)
      return nexusApi.updateBlobStore(bs.type, bs.name, { quotaBytes: bytes })
    },
    onSuccess: () => { qc.invalidateQueries({ queryKey: ['blobstores'] }); setEditingQuota(null) },
  })

  const isOnline = status?.status === 'ok'

  return (
    <div style={{ padding: 24, display: 'flex', flexDirection: 'column', gap: 24 }}>
      <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between' }}>
        <div style={{ marginBottom: 20 }}>
          <div className="holo-section-label" style={{ marginBottom: 4 }}>ADMINISTRATION / SYSTEM</div>
          <h1 style={{ fontSize: 20, fontWeight: 700, margin: '0 0 3px', letterSpacing: '-0.01em', lineHeight: 1.2, background: 'linear-gradient(110deg, #7c5cff, #22d3ee 60%)', WebkitBackgroundClip: 'text', WebkitTextFillColor: 'transparent', backgroundClip: 'text' as const }}>System Admin</h1>
          <p style={{ fontSize: 12, color: 'var(--holo-text-faint)', margin: 0 }}>Server health, blob stores and configuration</p>
        </div>
        <HoloButton onClick={() => { refetchStatus(); refetchBlobs(); refetchServices() }} aria-label="Refresh">
          <RefreshCw size={16} />
        </HoloButton>
      </div>

      {/* Tabs */}
      <HoloTabs
        items={[
          { value: 'info',       label: <><Info size={13} style={{ marginRight: 5 }} />Info</> },
          { value: 'blobs',      label: <><HardDrive size={13} style={{ marginRight: 5 }} />Blob Stores</> },
          { value: 'backup',     label: <><Database size={13} style={{ marginRight: 5 }} />Backup &amp; Restore</> },
          { value: 'monitoring', label: <><Activity size={13} style={{ marginRight: 5 }} />Monitoring</> },
          { value: 'migration',  label: <><ArrowRightLeft size={13} style={{ marginRight: 5 }} />Migration</> },
          { value: 'routing-rules', label: <><GitBranch size={13} style={{ marginRight: 5 }} />Routing Rules</> },
          { value: 'replication',   label: <><Share2 size={13} style={{ marginRight: 5 }} />Replication</> },
          { value: 'saml',          label: <><Shield size={13} style={{ marginRight: 5 }} />SAML SSO</> },
          { value: 'promotion',     label: <><ArrowUpCircle size={13} style={{ marginRight: 5 }} />Promotion</> },
        ] as HoloTabItem[]}
        value={tab}
        onChange={v => setTab(v as AdminTab)}
      />

      {/* Info */}
      {tab === 'info' && (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16 }}>
          {/* Status card */}
          <HoloCard>
            <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--holo-text-dim)', textTransform: 'uppercase', letterSpacing: '0.06em', marginBottom: 16, display: 'flex', alignItems: 'center', gap: 8 }}>
              <CheckCircle size={14} /> System Status
            </div>
            {statusLoading ? (
              <>
                <div className="holo-skeleton holo-skeleton--text" style={{ width: '70%' }} />
                <div className="holo-skeleton holo-skeleton--text" style={{ width: '60%', marginTop: 8 }} />
              </>
            ) : (
              <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 12 }}>
                <div style={{ width: 8, height: 8, borderRadius: '50%', background: isOnline ? '#22c55e' : '#ef4444', boxShadow: isOnline ? '0 0 6px #22c55e66' : '0 0 6px #ef444466', flexShrink: 0 }} />
                <span style={{ fontSize: 14, fontWeight: 600, color: isOnline ? '#22c55e' : '#ef4444' }}>
                  {isOnline ? 'Online' : 'Offline'}
                </span>
              </div>
            )}
          </HoloCard>

          {/* System Info card */}
          <HoloCard>
            <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--holo-text-dim)', textTransform: 'uppercase', letterSpacing: '0.06em', marginBottom: 16, display: 'flex', alignItems: 'center', gap: 8 }}>
              <Info size={14} /> System Info
            </div>
            {info ? (
              <>
                <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: '8px 0', borderBottom: '1px solid rgba(255,255,255,0.05)', fontSize: 13 }}>
                  <span style={{ color: 'var(--holo-text-dim)' }}>Product</span>
                  <span style={{ color: 'var(--holo-text)', fontWeight: 500 }}>{info.product}</span>
                </div>
                <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: '8px 0', borderBottom: '1px solid rgba(255,255,255,0.05)', fontSize: 13 }}>
                  <span style={{ color: 'var(--holo-text-dim)' }}>Version</span>
                  <span style={{ color: 'var(--holo-text)', fontWeight: 500 }}>{info.version}</span>
                </div>
              </>
            ) : (
              <>
                <div className="holo-skeleton holo-skeleton--text" style={{ width: '75%' }} />
                <div className="holo-skeleton holo-skeleton--text" style={{ width: '55%', marginTop: 8 }} />
                <div className="holo-skeleton holo-skeleton--text" style={{ width: '65%', marginTop: 8 }} />
              </>
            )}
          </HoloCard>
        </div>

        {/* Service Connections */}
        <HoloCard>
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 16 }}>
            <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--holo-text-dim)', textTransform: 'uppercase', letterSpacing: '0.06em', display: 'flex', alignItems: 'center', gap: 8 }}>
              <Wifi size={14} /> Service Connections
            </div>
            <HoloButton
              style={{ padding: '4px 8px' }}
              onClick={() => refetchServices()}
              disabled={servicesFetching}
              title="Re-run checks"
            >
              <RefreshCw size={13} style={{ animation: servicesFetching ? 'spin 1s linear infinite' : 'none' }} />
            </HoloButton>
          </div>
          {!services ? (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
              <div className="holo-skeleton holo-skeleton--text" style={{ width: '100%' }} />
              <div className="holo-skeleton holo-skeleton--text" style={{ width: '95%' }} />
              <div className="holo-skeleton holo-skeleton--text" style={{ width: '100%' }} />
            </div>
          ) : (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
              {services.map(svc => {
                const color = svc.status === 'ok' ? 'var(--holo-green)' : svc.status === 'error' ? 'var(--holo-red)' : svc.status === 'warn' ? 'var(--holo-amber)' : 'rgba(255,255,255,0.25)'
                const glow  = svc.status === 'ok' ? '0 0 5px var(--holo-green)' : svc.status === 'error' ? '0 0 5px var(--holo-red)' : svc.status === 'warn' ? '0 0 5px var(--holo-amber)' : 'none'
                return (
                  <div key={svc.name} style={{ display: 'grid', gridTemplateColumns: 'auto 1fr auto', alignItems: 'center', gap: 12, padding: '10px 12px', background: 'rgba(255,255,255,0.03)', borderRadius: 8, border: '1px solid rgba(255,255,255,0.06)' }}>
                    <span style={{ display: 'flex', alignItems: 'center', gap: 5 }}>
                      <span style={{ width: 7, height: 7, borderRadius: '50%', background: color, boxShadow: glow, flexShrink: 0, display: 'inline-block' }} />
                      <span style={{ fontSize: 10, fontWeight: 700, color, whiteSpace: 'nowrap' as const }}>{svc.status === 'ok' ? 'OK' : svc.status === 'error' ? 'ERROR' : svc.status === 'warn' ? 'WARN' : 'DISABLED'}</span>
                    </span>
                    <div style={{ minWidth: 0 }}>
                      <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--holo-text)', display: 'flex', alignItems: 'center', gap: 6 }}>
                        {svc.name}
                        {svc.name.startsWith('S3') && (
                          <span style={{ fontSize: 10, padding: '1px 6px', borderRadius: 4, background: 'rgba(245,158,11,0.15)', color: '#f59e0b', fontWeight: 700 }}>S3</span>
                        )}
                      </div>
                      <Truncated text={svc.detail} style={{ fontSize: 11, color: 'var(--holo-text-faint)', marginTop: 2 }} />
                    </div>
                    <div style={{ textAlign: 'right' as const, fontSize: 11, color: 'var(--holo-text-faint)', whiteSpace: 'nowrap' as const }}>
                      {svc.latency_ms != null && <span style={{ color: svc.latency_ms < 50 ? 'var(--holo-green)' : svc.latency_ms < 200 ? 'var(--holo-amber)' : 'var(--holo-red)' }}>{svc.latency_ms}ms</span>}
                      <div style={{ marginTop: 2 }}>{new Date(svc.checked_at).toLocaleTimeString()}</div>
                    </div>
                  </div>
                )
              })}
            </div>
          )}
        </HoloCard>

        {/* Docker Subdomain Connector */}
        {(() => {
          const connector = services?.find(s => s.name === 'Docker Subdomain Connector')
          if (!connector) return null
          const statusColor =
            connector.status === 'ok'       ? 'var(--holo-green)'    :
            connector.status === 'warn'     ? 'var(--holo-amber)'    :
            connector.status === 'disabled' ? 'var(--holo-text-dim)' :
                                              'var(--holo-red)'
          const statusLabel =
            connector.status === 'ok'       ? 'ACTIVE'   :
            connector.status === 'warn'     ? 'WARN'     :
            connector.status === 'disabled' ? 'DISABLED' : 'ERROR'
          const baseDomain = connector.detail.match(/\*\.([^\s]+)/)?.[1]
          return (
            <HoloCard>
              <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--holo-text-dim)', textTransform: 'uppercase', letterSpacing: '0.06em', marginBottom: 12, display: 'flex', alignItems: 'center', gap: 8 }}>
                <Network size={14} style={{ color: 'var(--holo-primary)' }} />
                Docker Subdomain Connector
                <span style={{ marginLeft: 'auto', fontSize: 11, fontWeight: 700, color: statusColor }}>{statusLabel}</span>
              </div>
              <div style={{ fontSize: 12, color: 'var(--holo-text-dim)', lineHeight: 1.6 }}>{connector.detail}</div>
              {connector.status === 'ok' && baseDomain && (
                <div style={{ marginTop: 12, padding: '8px 12px', background: 'rgba(59,130,246,0.08)', borderRadius: 8, fontFamily: 'monospace', fontSize: 11, color: 'var(--holo-text)' }}>
                  docker pull <span style={{ color: 'var(--holo-primary)' }}>&lt;repo&gt;</span>.{baseDomain}/<span style={{ color: 'var(--holo-primary)' }}>image:tag</span>
                </div>
              )}
              {connector.status === 'disabled' && (
                <div style={{ marginTop: 10, fontSize: 11, color: 'var(--holo-text-dim)' }}>
                  Set <code style={{ background: 'rgba(255,255,255,0.06)', padding: '1px 4px', borderRadius: 3 }}>docker.subdomain_connector.enabled: true</code> in <code style={{ background: 'rgba(255,255,255,0.06)', padding: '1px 4px', borderRadius: 3 }}>config.yaml</code>
                </div>
              )}
            </HoloCard>
          )
        })()}
        </div>
      )}

      {/* Backup / Restore */}
      {tab === 'backup' && (
      <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>

        {/* ── System Backup & Restore ── */}
        <HoloCard style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 2 }}>
            <Database size={15} style={{ color: 'var(--holo-text-dim)' }} />
            <span style={{ fontSize: 14, fontWeight: 600, color: 'var(--holo-text)' }}>System Backup &amp; Restore</span>
          </div>
          <p style={{ fontSize: 12, color: 'var(--holo-text-faint)', margin: 0 }}>
            Full instance snapshot — all repositories, users, roles, policies, components, assets and blobs.
            Restore is non-destructive; existing records are skipped.
          </p>
          <div style={{ display: 'flex', gap: 10, flexWrap: 'wrap' as const, alignItems: 'center' }}>
            <HoloButton variant="primary" icon={<Download size={14} />} onClick={handleExport} disabled={exportBusy}>
              {exportBusy ? 'Exporting…' : 'Export Backup'}
            </HoloButton>
            <HoloButton icon={<Upload size={14} />} onClick={() => fileInputRef.current?.click()} disabled={restoreBusy}>
              {restoreBusy ? 'Restoring…' : 'Restore'}
            </HoloButton>
            <input ref={fileInputRef} type="file" accept=".tar.gz,.tgz" style={{ display: 'none' }} onChange={handleRestore} />
          </div>
          {restoreError && (
            <div role="alert" style={{ background: 'rgba(255,107,107,0.08)', border: '1px solid rgba(239,68,68,0.3)', borderRadius: 8, padding: '10px 14px', color: 'var(--holo-red)', fontSize: 13 }}>
              {restoreError}
            </div>
          )}
          {restoreResult && (
            <div style={{ background: 'rgba(34,197,94,0.08)', border: '1px solid rgba(34,197,94,0.25)', borderRadius: 8, padding: '10px 14px', fontSize: 13 }}>
              <span style={{ color: 'var(--holo-green)', fontWeight: 600, marginBottom: 6, display: 'block' }}>Restore complete</span>
              <div style={{ display: 'flex', gap: 16, flexWrap: 'wrap' as const }}>
                {Object.entries(restoreResult).map(([k, v]) => (
                  <span key={k} style={{ color: 'rgba(229,231,235,0.7)' }}>
                    <span style={{ color: 'var(--holo-text)', fontWeight: 600 }}>{v}</span> {k}
                  </span>
                ))}
              </div>
            </div>
          )}
        </HoloCard>

        {/* ── Repository Import ── */}
        <HoloCard style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 2 }}>
            <Archive size={15} style={{ color: 'var(--holo-text-dim)' }} />
            <span style={{ fontSize: 14, fontWeight: 600, color: 'var(--holo-text)' }}>Repository Import</span>
          </div>
          <p style={{ fontSize: 12, color: 'var(--holo-text-faint)', margin: 0 }}>
            Import a single repository from a <code style={{ color: 'var(--holo-a)' }}>.tar.gz</code> archive
            exported from this or another Nexspence instance. Users, roles and cleanup policies are not included — only repository metadata, components, assets and blobs.
          </p>

          <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
            {/* File picker */}
            <div>
              <label style={{ fontSize: 12, color: 'var(--holo-text-faint)', display: 'block', marginBottom: 6 }}>Archive file</label>
              <input
                ref={importFileRef}
                type="file"
                accept=".tar.gz,.tgz"
                style={{ display: 'none' }}
                onChange={e => {
                  setImportFile(e.target.files?.[0] ?? null)
                  setImportResult(null)
                  setImportError(null)
                }}
              />
              <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
                <HoloButton icon={<Paperclip size={14} />} onClick={() => importFileRef.current?.click()}>
                  Choose archive
                </HoloButton>
                {importFile ? (
                  <span style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 12, color: 'var(--holo-text-faint)', maxWidth: 320, overflow: 'hidden' }}>
                    <Archive size={12} style={{ flexShrink: 0 }} />
                    <Truncated as="span" text={importFile.name} />
                    <button
                      onClick={() => { setImportFile(null); if (importFileRef.current) importFileRef.current.value = '' }}
                      style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'var(--holo-text-dim)', padding: '0 2px', lineHeight: 1, fontSize: 16, flexShrink: 0 }}
                      title="Clear"
                    ><X size={13} /></button>
                  </span>
                ) : (
                  <span style={{ fontSize: 12, color: 'rgba(229,231,235,0.28)' }}>No file selected</span>
                )}
              </div>
            </div>

            {/* Target name */}
            <div>
              <label style={{ fontSize: 12, color: 'var(--holo-text-faint)', display: 'block', marginBottom: 6 }}>
                Target name <span style={{ color: 'rgba(229,231,235,0.35)' }}>— optional, overrides the name in the archive</span>
              </label>
              <HoloInput
                placeholder="leave blank to use the archived name"
                value={importTargetName}
                onChange={e => setImportTargetName(e.target.value)}
                style={{ width: 300 }}
              />
            </div>

            {/* Conflict mode */}
            <div>
              <label style={{ fontSize: 12, color: 'var(--holo-text-faint)', display: 'block', marginBottom: 6 }}>Conflict mode</label>
              <Select
                value={importConflict}
                onChange={setImportConflict}
                options={[
                  { value: 'skip',   label: 'Skip — add only absent components/assets if repo exists' },
                  { value: 'rename', label: 'Rename — create under target name (fails if name is taken)' },
                ]}
                style={{ width: 360 }}
              />
            </div>

            <div>
              <HoloButton
                variant="primary"
                icon={<Upload size={14} />}
                disabled={!importFile || importBusy}
                onClick={handleImportRepo}
              >
                {importBusy ? 'Importing…' : 'Import Repository'}
              </HoloButton>
            </div>

            {importResult && (
              <div style={{ padding: '10px 14px', borderRadius: 8, background: 'rgba(34,197,94,0.08)', border: '1px solid rgba(34,197,94,0.28)', fontSize: 13, color: 'var(--holo-text)' }}>
                <span style={{ color: 'var(--holo-green)', fontWeight: 600, display: 'block', marginBottom: 4 }}>Import complete</span>
                Imported <strong>{importResult.imported.components}</strong> components and{' '}
                <strong>{importResult.imported.assets}</strong> assets into{' '}
                <code style={{ color: '#93c5fd' }}>{importResult.imported.repository}</code>
                {importResult.imported.blobs > 0 && <>, <strong>{importResult.imported.blobs}</strong> blobs</>}.
              </div>
            )}
            {importError && (
              <div role="alert" style={{ padding: '10px 14px', borderRadius: 8, background: 'rgba(239,68,68,0.08)', border: '1px solid rgba(239,68,68,0.28)', fontSize: 13, color: '#fca5a5' }}>
                {importError}
              </div>
            )}
          </div>
        </HoloCard>

      </div>
      )}

      {/* Blob stores */}
      {tab === 'blobs' && (
      <div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 12 }}>
          <HardDrive size={15} style={{ color: 'var(--holo-text-dim)' }} />
          <span style={{ fontSize: 15, fontWeight: 600, color: 'var(--holo-text)' }}>Blob Stores</span>
          <span style={{ fontSize: 12, color: 'var(--holo-text-faint)', marginLeft: 4 }}>
            {blobs.length} total
          </span>
          <HoloButton variant="primary" icon={<Plus size={14} />} style={{ marginLeft: 'auto' }} onClick={() => setCreateOpen(true)}>New Blob Store</HoloButton>
        </div>

        {blobsLoading ? (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
            <div className="holo-skeleton holo-skeleton--block" />
            <div className="holo-skeleton holo-skeleton--block" />
          </div>
        ) : blobs.length === 0 ? (
          <div style={{ background: 'rgba(255,255,255,0.03)', border: '1px solid rgba(255,255,255,0.08)', borderRadius: 14, padding: '40px 20px', textAlign: 'center' as const, color: 'var(--holo-text-faint)', fontSize: 14, display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 8 }}>
            <Database size={40} style={{ opacity: 0.3 }} />
            <div style={{ fontWeight: 500, color: 'var(--holo-text)' }}>No blob stores configured</div>
            <div style={{ fontSize: 12 }}>Create a blob store to manage artifact storage locations.</div>
            <HoloButton variant="primary" icon={<Plus size={14} />} style={{ marginTop: 4 }} onClick={() => setCreateOpen(true)}>New Blob Store</HoloButton>
          </div>
        ) : (
          <div style={{ background: 'rgba(255,255,255,0.02)', border: '1px solid var(--holo-border)', borderRadius: 12, overflow: 'hidden' }}>
            <div style={{ display: 'grid', gridTemplateColumns: '2fr 1fr 2fr 1fr', padding: '10px 16px', background: 'rgba(255,255,255,0.03)', borderBottom: '1px solid var(--holo-border)', fontSize: 11, fontWeight: 600, color: 'var(--holo-text-faint)', textTransform: 'uppercase' as const, letterSpacing: '0.05em' }}>
              <div>Name</div>
              <div>Type</div>
              <div>Used</div>
              <div>Quota</div>
            </div>
            {blobs.map(bs => {
              const usedPct = bs.quotaBytes ? Math.min((bs.usedBytes / bs.quotaBytes) * 100, 100) : 0
              const overQuota = bs.quotaBytes && bs.usedBytes > bs.quotaBytes
              const barColor = overQuota ? '#ef4444' : usedPct > 80 ? '#f59e0b' : 'var(--holo-a)'
              const isEditing = editingQuota === bs.id
              return (
                <div
                  key={bs.id}
                  tabIndex={0}
                  style={{ display: 'grid', gridTemplateColumns: '2fr 1fr 2fr 1fr', padding: '11px 16px', borderBottom: '1px solid rgba(255,255,255,0.05)', fontSize: 13, color: 'var(--holo-text)', alignItems: 'center', cursor: 'pointer' }}
                  onClick={(e) => {
                    // Don't open detail if click is on a button or input
                    const t = e.target as HTMLElement
                    if (t.closest('button') || t.closest('input')) return
                    setDetailName(bs.name)
                  }}
                  onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); setDetailName(bs.name) } }}
                >
                  <div style={{ fontWeight: 600, color: 'var(--holo-text)', display: 'flex', alignItems: 'center' }}>
                    {bs.name}
                    {bs.type === 'group' && (
                      <span style={{
                        fontSize: 10, fontWeight: 700, padding: '2px 6px',
                        borderRadius: 4, background: 'rgba(139,92,246,0.15)',
                        color: '#a78bfa', border: '1px solid rgba(139,92,246,0.3)',
                        marginLeft: 6, letterSpacing: '0.05em',
                      }}>GROUP</span>
                    )}
                  </div>
                  <div>
                    <span style={{ fontSize: 11, fontWeight: 600, padding: '2px 8px', borderRadius: 4, background: 'rgba(124,92,255,0.15)', color: 'var(--holo-a)' }}>
                      {bs.type}
                    </span>
                  </div>
                  <div>
                    <div style={{ fontSize: 13 }}>{fmtBytes(bs.usedBytes)}</div>
                    {bs.quotaBytes && (
                      <div style={{ height: 4, borderRadius: 2, background: 'rgba(255,255,255,0.08)', overflow: 'hidden', marginTop: 4, width: '100%' }}>
                        <div style={{ height: '100%', width: usedPct + '%', background: barColor, transition: 'width 0.3s' }} />
                      </div>
                    )}
                  </div>
                  <div>
                    {isEditing ? (
                      <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                        <HoloInput
                          type="number" min="0" step="0.1" autoFocus
                          value={quotaInput}
                          onChange={e => setQuotaInput(e.target.value)}
                          placeholder="GB"
                          style={{ width: 72 }}
                          onKeyDown={e => {
                            if (e.key === 'Enter') quotaMut.mutate({ bs, gb: quotaInput })
                            if (e.key === 'Escape') setEditingQuota(null)
                          }}
                        />
                        <span style={{ fontSize: 11, color: 'var(--holo-text-faint)' }}>GB</span>
                        <button
                          style={{ background: 'rgba(34,197,94,0.15)', border: '1px solid rgba(34,197,94,0.3)', borderRadius: 6, padding: '3px 8px', color: '#22c55e', fontSize: 11, cursor: 'pointer' }}
                          onClick={() => quotaMut.mutate({ bs, gb: quotaInput })}
                        >Save</button>
                        <button
                          style={{ background: 'none', border: 'none', color: 'var(--holo-text-faint)', cursor: 'pointer', padding: 2 }}
                          onClick={() => setEditingQuota(null)}
                        ><X size={12} /></button>
                      </div>
                    ) : (
                      <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                        <span style={overQuota ? { color: '#ef4444', fontSize: 12 } : { fontSize: 12, color: 'var(--holo-text-faint)' }}>
                          {bs.quotaBytes ? fmtBytes(bs.quotaBytes) : 'Unlimited'}
                        </span>
                        <button
                          title="Edit quota"
                          style={{ background: 'none', border: 'none', color: 'var(--holo-text-faint)', cursor: 'pointer', padding: 2, display: 'flex', alignItems: 'center' }}
                          onClick={() => {
                            setEditingQuota(bs.id)
                            setQuotaInput(bs.quotaBytes ? (bs.quotaBytes / 1024 / 1024 / 1024).toFixed(1) : '')
                          }}
                        ><Pencil size={11} /></button>
                      </div>
                    )}
                  </div>
                </div>
              )
            })}
          </div>
        )}
      </div>
      )}

      {/* Monitoring */}
      {tab === 'monitoring' && (
        <Suspense fallback={<div style={{ color: 'var(--holo-text-faint)', fontSize: 13, padding: 24 }}>Loading…</div>}>
          <MonitoringView />
        </Suspense>
      )}

      {/* Migration */}
      {tab === 'migration' && <MigrationTab />}

      {/* Routing Rules */}
      {tab === 'routing-rules' && <RoutingRulesTab />}

      {/* Replication */}
      {tab === 'replication' && <ReplicationTab />}

      {/* SAML SSO */}
      {tab === 'saml' && <SamlTab />}

      {/* Promotion */}
      {tab === 'promotion' && <PromotionTab />}

      {detailName && <BlobStoreDetailModal name={detailName} blobStores={blobs} onClose={() => setDetailName(null)} />}
      {createOpen && <CreateBlobStoreModal blobStores={blobs} onClose={() => setCreateOpen(false)} />}
    </div>
  )
}

// ── BlobStoreDetailModal ─────────────────────────────────────────
function BlobStoreDetailModal({ name, blobStores: _blobStores, onClose }: { name: string; blobStores: BlobStore[]; onClose: () => void }) {
  const qc = useQueryClient()
  const { data, isLoading, error } = useQuery<UsageResp>({
    queryKey: ['blobstore-usage', name],
    queryFn: () => nexusApi.getBlobStoreUsage(name).then(r => r.data),
  })
  const linked = data?.linkedRepositories ?? []
  const bs = data?.store
  const used = bs ? fmtBytes(bs.usedBytes) : '—'
  const quota = bs?.quotaBytes ? fmtBytes(bs.quotaBytes) : 'Unlimited'
  const remaining = data?.quotaRemaining !== undefined ? fmtBytes(data.quotaRemaining) : null
  const canDelete = linked.length === 0

  const [deleteError, setDeleteError] = useState('')
  const [editing, setEditing] = useState(false)
  const [editBucket, setEditBucket]       = useState('')
  const [editRegion, setEditRegion]       = useState('')
  const [editEndpoint, setEditEndpoint]   = useState('')
  const [editAccessKey, setEditAccessKey] = useState('')
  const [editSecretKey, setEditSecretKey] = useState('')
  const [editSkipTLS, setEditSkipTLS]     = useState(false)
  const [editPath, setEditPath]           = useState('')
  const [editErr, setEditErr]             = useState('')
  const delMut = useMutation({
    mutationFn: () => nexusApi.deleteBlobStore(name),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['blobstores'] })
      onClose()
    },
    onError: (err: unknown) => {
      const msg = (err as { response?: { data?: { error?: string } } })?.response?.data?.error ?? 'Delete failed'
      setDeleteError(msg)
    },
  })

  const editMut = useMutation({
    mutationFn: () => {
      if (!bs) return Promise.reject('no store')
      // The API never sends the secret key back (only a secret_key_set marker), so an
      // untouched field must omit it entirely — the server then keeps the stored one.
      const config: Record<string, unknown> = bs.type === 's3'
        ? { bucket: editBucket, region: editRegion, endpoint: editEndpoint,
            access_key: editAccessKey, skip_tls_verify: editSkipTLS,
            ...(editSecretKey ? { secret_key: editSecretKey } : {}) }
        : { path: editPath }
      return nexusApi.updateBlobStore(bs.type, bs.name, { config, quotaBytes: bs.quotaBytes ?? null })
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['blobstore-usage', name] })
      qc.invalidateQueries({ queryKey: ['blobstores'] })
      setEditing(false)
    },
    onError: (e: unknown) => {
      const msg = (e as { response?: { data?: { error?: string } } })?.response?.data?.error ?? 'Save failed'
      setEditErr(msg)
    },
  })

  const [gcResult, setGcResult] = useState<null | {
    scannedBlobs: number; orphans: number; freedBytes: number; dryRun: boolean
  }>(null)
  const [gcError, setGcError] = useState('')
  const gcMut = useMutation({
    mutationFn: (dryRun: boolean) =>
      nexusApi.compactBlobStore(name, { dryRun }).then(r => r.data as {
        scannedBlobs: number; orphans: number; freedBytes: number; dryRun: boolean
      }),
    onSuccess: (res) => {
      setGcResult(res)
      setGcError('')
      if (!res.dryRun) {
        qc.invalidateQueries({ queryKey: ['blobstore-usage', name] })
        qc.invalidateQueries({ queryKey: ['blobstores'] })
      }
    },
    onError: (e: unknown) => {
      const msg = (e as { response?: { data?: { error?: string } } })?.response?.data?.error ?? 'GC failed'
      setGcError(msg)
    },
  })

  const startEdit = () => {
    const cfg = bs?.config ?? {}
    setEditBucket((cfg.bucket as string) ?? '')
    setEditRegion((cfg.region as string) ?? 'us-east-1')
    setEditEndpoint((cfg.endpoint as string) ?? '')
    setEditAccessKey((cfg.access_key as string) ?? '')
    setEditSecretKey('')
    setEditSkipTLS(cfg.skip_tls_verify === true)
    setEditPath((cfg.path as string) ?? '')
    setEditErr('')
    setEditing(true)
  }

  return (
    <ModalShell title={`Blob Store: ${name}`} onClose={onClose} width={640}>
      {isLoading && (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
          <div className="holo-skeleton holo-skeleton--text" style={{ width: '60%' }} />
          <div className="holo-skeleton holo-skeleton--block" />
        </div>
      )}
      {error && <p style={{ color: 'var(--holo-red)' }}>Failed to load usage</p>}
      {bs && (
        <>
          <div style={{ display: 'grid', gridTemplateColumns: 'auto 1fr', gap: '6px 14px', fontSize: 13, marginBottom: 16 }}>
            <span style={{ color: 'var(--holo-text-dim)' }}>Type</span>
            <span style={{ color: 'var(--holo-text)' }}>{bs.type}</span>
            <span style={{ color: 'var(--holo-text-dim)' }}>Used</span>
            <span style={{ color: 'var(--holo-text)' }}>{used}</span>
            <span style={{ color: 'var(--holo-text-dim)' }}>Quota</span>
            <span style={{ color: 'var(--holo-text)' }}>{quota}</span>
            {remaining !== null && (
              <>
                <span style={{ color: 'var(--holo-text-dim)' }}>Remaining</span>
                <span style={{ color: 'var(--holo-text)' }}>{remaining}</span>
              </>
            )}
            <span style={{ color: 'var(--holo-text-dim)' }}>Asset total</span>
            <span style={{ color: 'var(--holo-text)' }}>{fmtBytes(data?.totalAssetBytes ?? 0)} across {linked.length} {linked.length === 1 ? 'repo' : 'repos'}</span>
            {bs.type === 's3' && bs.config && (
              <>
                <span style={{ color: 'var(--holo-text-dim)' }}>Endpoint</span>
                <span style={{ color: 'var(--holo-text)', fontFamily: 'monospace', fontSize: 12 }}>
                  {(bs.config.endpoint as string) || 'AWS S3'}
                </span>
                <span style={{ color: 'var(--holo-text-dim)' }}>Bucket</span>
                <span style={{ color: 'var(--holo-text)', fontFamily: 'monospace', fontSize: 12 }}>
                  {(bs.config.bucket as string) || '—'}
                </span>
                <span style={{ color: 'var(--holo-text-dim)' }}>Region</span>
                <span style={{ color: 'var(--holo-text)', fontFamily: 'monospace', fontSize: 12 }}>
                  {(bs.config.region as string) || '—'}
                </span>
              </>
            )}
            {bs.type === 'local' && bs.config && (
              <>
                <span style={{ color: 'var(--holo-text-dim)' }}>Path</span>
                <span style={{ color: 'var(--holo-text)', fontFamily: 'monospace', fontSize: 12 }}>
                  {(bs.config.path as string) || '—'}
                </span>
              </>
            )}
          </div>

          {bs.type === 'group' && (
            <div style={{ marginTop: 16 }}>
              <div style={{ color: '#94a3b8', fontSize: 12, marginBottom: 8 }}>
                Fill Policy: <strong style={{ color: '#e2e8f0' }}>
                  {bs.config?.fill_policy === 'write_to_first_fill' ? 'Write to First Fill' : 'Round Robin'}
                </strong>
              </div>
              {data?.memberTotalUsed !== undefined && (
                <div style={{ color: '#94a3b8', fontSize: 12, marginBottom: 8 }}>
                  Total used: <strong style={{ color: '#e2e8f0' }}>
                    {(data.memberTotalUsed / 1024 / 1024).toFixed(1)} MB
                  </strong>
                  {data.memberTotalQuota !== undefined && (
                    <> / {(data.memberTotalQuota / 1024 / 1024).toFixed(1)} MB</>
                  )}
                </div>
              )}
              <div style={{ color: '#94a3b8', fontSize: 12, marginBottom: 6 }}>Members:</div>
              {(data?.members ?? []).map(m => (
                <div key={m.id} style={{ display: 'flex', justifyContent: 'space-between',
                  fontSize: 12, padding: '4px 8px', background: 'rgba(255,255,255,0.04)', borderRadius: 6, marginBottom: 4 }}>
                  <span style={{ color: '#e2e8f0' }}>{m.name}</span>
                  <span style={{ color: '#64748b' }}>
                    {(m.usedBytes / 1024 / 1024).toFixed(1)} MB
                    {m.quotaBytes != null ? ` / ${(m.quotaBytes / 1024 / 1024).toFixed(1)} MB` : ''}
                  </span>
                </div>
              ))}
            </div>
          )}

          {editing && bs && (
            <div style={{ marginBottom: 16, padding: '12px 14px', background: 'rgba(255,255,255,0.03)', borderRadius: 10, border: '1px solid var(--holo-border)' }}>
              <div style={{ fontSize: 12, fontWeight: 600, color: 'var(--holo-text-dim)', textTransform: 'uppercase' as const, letterSpacing: '0.05em', marginBottom: 10 }}>Edit Configuration</div>
              {bs.type === 's3' ? (
                <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                  <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 8 }}>
                    <div>
                      <label style={{ fontSize: 11, color: 'var(--holo-text-faint)', display: 'block', marginBottom: 3 }}>Bucket</label>
                      <HoloInput value={editBucket} onChange={e => setEditBucket(e.target.value)} />
                    </div>
                    <div>
                      <label style={{ fontSize: 11, color: 'var(--holo-text-faint)', display: 'block', marginBottom: 3 }}>Region</label>
                      <HoloInput value={editRegion} onChange={e => setEditRegion(e.target.value)} />
                    </div>
                  </div>
                  <div>
                    <label style={{ fontSize: 11, color: 'var(--holo-text-faint)', display: 'block', marginBottom: 3 }}>Endpoint</label>
                    <HoloInput value={editEndpoint} onChange={e => setEditEndpoint(e.target.value)} placeholder="leave empty for AWS S3" />
                  </div>
                  <label style={{ display: 'flex', alignItems: 'flex-start', gap: 8, cursor: 'pointer', fontSize: 12, color: 'var(--holo-text)' }}>
                    <input type="checkbox" checked={editSkipTLS} onChange={e => setEditSkipTLS(e.target.checked)} style={{ marginTop: 2 }} />
                    <span>
                      Skip TLS certificate verification
                      <span style={{ display: 'block', color: 'var(--holo-text-faint)', fontSize: 11 }}>
                        For an endpoint behind a private CA. Credentials and every blob travel over an unverified connection.
                      </span>
                    </span>
                  </label>
                  <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 8 }}>
                    <div>
                      <label style={{ fontSize: 11, color: 'var(--holo-text-faint)', display: 'block', marginBottom: 3 }}>Access Key</label>
                      <HoloInput value={editAccessKey} onChange={e => setEditAccessKey(e.target.value)} />
                    </div>
                    <div>
                      <label style={{ fontSize: 11, color: 'var(--holo-text-faint)', display: 'block', marginBottom: 3 }}>Secret Key (leave blank to keep)</label>
                      <HoloInput type="password" value={editSecretKey} onChange={e => setEditSecretKey(e.target.value)} placeholder="unchanged" />
                      <div style={{ fontSize: 11, color: 'var(--holo-text-faint)', marginTop: 3 }}>
                        {bs.config?.secret_key_set
                          ? 'A secret key is stored. It is never sent to the browser — leave this blank to keep it.'
                          : 'No secret key stored; the store falls back to the instance IAM role.'}
                      </div>
                    </div>
                  </div>
                </div>
              ) : (
                <div>
                  <label style={{ fontSize: 11, color: 'var(--holo-text-faint)', display: 'block', marginBottom: 3 }}>Path</label>
                  <HoloInput value={editPath} onChange={e => setEditPath(e.target.value)} />
                </div>
              )}
              {editErr && <div style={{ marginTop: 8, color: 'var(--holo-red)', fontSize: 12 }}>{editErr}</div>}
              <div style={{ display: 'flex', gap: 8, marginTop: 10 }}>
                <HoloButton variant="primary" disabled={editMut.isPending} onClick={() => editMut.mutate()}>
                  {editMut.isPending ? 'Saving…' : 'Save'}
                </HoloButton>
                <HoloButton onClick={() => { setEditing(false); setEditErr('') }}>Cancel</HoloButton>
              </div>
            </div>
          )}

          {bs.type !== 'group' && (
            <div style={{ marginBottom: 16, padding: '12px 14px', background: 'rgba(255,255,255,0.03)', borderRadius: 10, border: '1px solid var(--holo-border)' }}>
              <div style={{ fontSize: 12, fontWeight: 600, color: 'var(--holo-text-faint)', textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: 8 }}>
                Garbage collection
              </div>
              <p style={{ fontSize: 12, color: 'var(--holo-text-dim)', margin: '0 0 10px' }}>
                Remove blobs no longer referenced by any asset (older than the grace period).
              </p>
              <div style={{ display: 'flex', gap: 8 }}>
                <HoloButton disabled={gcMut.isPending} onClick={() => gcMut.mutate(true)}>
                  {gcMut.isPending ? 'Scanning…' : 'Dry run'}
                </HoloButton>
                <HoloButton
                  variant="danger"
                  disabled={gcMut.isPending}
                  onClick={() => { if (confirm(`Run garbage collection on "${name}"? Orphaned blobs will be deleted.`)) gcMut.mutate(false) }}
                >
                  Compact
                </HoloButton>
              </div>
              {gcResult && (
                <div role="status" style={{ marginTop: 10, fontSize: 12, color: 'var(--holo-text)' }}>
                  {gcResult.dryRun ? 'Dry run: ' : 'Collected: '}
                  {gcResult.orphans} orphans ({fmtBytes(gcResult.freedBytes)}) of {gcResult.scannedBlobs} scanned.
                </div>
              )}
              {gcError && (
                <div role="alert" style={{ marginTop: 8, color: 'var(--holo-red)', fontSize: 12 }}>{gcError}</div>
              )}
            </div>
          )}

          <div style={{ fontSize: 12, fontWeight: 600, color: 'var(--holo-text-faint)', textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: 8 }}>
            Linked Repositories
          </div>
          {linked.length === 0 ? (
            <div style={{ padding: '20px 16px', background: 'rgba(255,255,255,0.02)', border: '1px dashed var(--holo-border)', borderRadius: 8, textAlign: 'center', color: 'var(--holo-text-faint)', fontSize: 13 }}>
              No repositories use this blob store.
            </div>
          ) : (
            <div style={{ background: 'rgba(255,255,255,0.02)', border: '1px solid var(--holo-border)', borderRadius: 10, overflow: 'hidden' }}>
              <div style={{ display: 'grid', gridTemplateColumns: '2fr 1fr 1fr 1fr', padding: '8px 14px', background: 'rgba(255,255,255,0.03)', fontSize: 11, fontWeight: 600, color: 'var(--holo-text-faint)', textTransform: 'uppercase', letterSpacing: '0.05em' }}>
                <div>Name</div><div>Format</div><div>Type</div><div>Used</div>
              </div>
              {linked.map(r => (
                <Link key={r.name} to={`/browse?repo=${encodeURIComponent(r.name)}`} style={{ textDecoration: 'none' }}>
                  <div style={{ display: 'grid', gridTemplateColumns: '2fr 1fr 1fr 1fr', padding: '10px 14px', borderTop: '1px solid rgba(255,255,255,0.05)', fontSize: 13, color: 'var(--holo-text)', cursor: 'pointer' }}>
                    <div style={{ color: 'var(--holo-a)', fontWeight: 600 }}>{r.name}</div>
                    <div>{r.format}</div>
                    <div>{r.type}</div>
                    <div>{fmtBytes(r.bytesUsed)}</div>
                  </div>
                </Link>
              ))}
            </div>
          )}

          {deleteError && (
            <div role="alert" style={{ marginTop: 12, background: 'rgba(255,107,107,0.08)', border: '1px solid rgba(239,68,68,0.3)', borderRadius: 8, padding: '8px 12px', color: 'var(--holo-red)', fontSize: 13 }}>
              {deleteError}
            </div>
          )}

          <div style={{ marginTop: 20, display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
            <HoloButton
              variant="danger"
              disabled={!canDelete || delMut.isPending}
              title={canDelete ? 'Delete this blob store' : 'Detach all repositories first'}
              onClick={() => {
                setDeleteError('')
                if (confirm(`Delete blob store "${name}"? This cannot be undone.`)) delMut.mutate()
              }}
            >
              <Trash2 size={13} />
              {delMut.isPending ? 'Deleting…' : 'Delete'}
            </HoloButton>
            <div style={{ display: 'flex', gap: 8 }}>
              {!editing && bs && bs.type !== 'group' && (
                <HoloButton icon={<Pencil size={13} />} onClick={startEdit}>Edit Config</HoloButton>
              )}
              <HoloButton onClick={onClose}>Close</HoloButton>
            </div>
          </div>
        </>
      )}
    </ModalShell>
  )
}

// ── CreateBlobStoreModal ──────────────────────────────────────────
function CreateBlobStoreModal({ blobStores, onClose }: { blobStores: BlobStore[]; onClose: () => void }) {
  const qc = useQueryClient()
  const [name, setName] = useState('')
  const [type, setType] = useState<'local' | 's3' | 'group'>('local')
  const [path, setPath] = useState('./data/blobs/')
  const [bucket, setBucket] = useState('')
  const [region, setRegion] = useState('us-east-1')
  const [endpoint, setEndpoint] = useState('')
  const [prefix, setPrefix] = useState('')
  const [accessKey, setAccessKey] = useState('')
  const [secretKey, setSecretKey] = useState('')
  const [skipTLS, setSkipTLS] = useState(false)
  const [quotaGB, setQuotaGB] = useState('')
  const [err, setErr] = useState('')
  const [testResult, setTestResult] = useState<{ ok: boolean; error?: string } | null>(null)
  const [testBusy, setTestBusy] = useState(false)
  // Bumped by handleTest and by every field onChange that feeds its config.
  // A field edited after a test started must not let that test's late
  // response land as if it verified the (now different) config a moment
  // later — same request-sequence-guard pattern issue 12 applies to
  // SecurityPage.tsx's openEdit.
  const testReqSeq = useRef(0)
  const [groupFillPolicy, setGroupFillPolicy] = useState<'round_robin' | 'write_to_first_fill'>('round_robin')
  const [groupMemberIds, setGroupMemberIds] = useState<string[]>([])

  const mut = useMutation({
    mutationFn: () => {
      const quotaBytes = quotaGB.trim() === '' ? null : Math.round(parseFloat(quotaGB) * 1024 * 1024 * 1024)
      const config: Record<string, unknown> = type === 'local'
        ? { path }
        : type === 's3'
        ? { bucket, region, endpoint, prefix, access_key: accessKey, secret_key: secretKey, skip_tls_verify: skipTLS }
        : {}
      if (type === 'group') {
        config.fill_policy = groupFillPolicy
        config.member_ids = groupMemberIds
      }
      return nexusApi.createBlobStore(type, { name, config, quotaBytes })
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['blobstores'] })
      setGroupFillPolicy('round_robin')
      setGroupMemberIds([])
      onClose()
    },
    onError: (e: unknown) => {
      const msg = (e as { response?: { data?: { error?: string } } })?.response?.data?.error ?? 'Create failed'
      setErr(msg)
    },
  })

  const handleTest = async () => {
    const seq = ++testReqSeq.current
    setTestBusy(true)
    setTestResult(null)
    try {
      const cfg: Record<string, unknown> = type === 'local'
        ? { path }
        : { bucket, region, endpoint, prefix, access_key: accessKey, secret_key: secretKey, skip_tls_verify: skipTLS }
      const res = await nexusApi.testBlobStore(type === 'group' ? 'local' : type, cfg)
      if (seq !== testReqSeq.current) return // a field changed since this test started
      setTestResult(res.data)
    } catch {
      if (seq !== testReqSeq.current) return
      setTestResult({ ok: false, error: 'Request failed' })
    } finally {
      // Unlike setTestResult above, this must run regardless of seq: nothing
      // else ever clears testBusy for a superseded request (no new test was
      // started), so gating it the same way would leave the button stuck
      // showing "Testing…" forever after an edit invalidates an in-flight one.
      setTestBusy(false)
    }
  }

  return (
    <ModalShell title="New Blob Store" onClose={onClose} width={500}>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
        <div>
          <label style={{ fontSize: 12, fontWeight: 600, color: 'var(--holo-text-dim)', marginBottom: 4, display: 'block' }}>Name</label>
          <HoloInput value={name} onChange={e => setName(e.target.value)} placeholder="e.g. fast-ssd" autoFocus />
        </div>
        <div>
          <label style={{ fontSize: 12, fontWeight: 600, color: 'var(--holo-text-dim)', marginBottom: 4, display: 'block' }}>Type</label>
          <Select
            value={type}
            onChange={v => { testReqSeq.current++; setType(v as 'local' | 's3' | 'group'); setTestResult(null) }}
            options={[{ value: 'local', label: 'Local filesystem' }, { value: 's3', label: 'S3-compatible' }, { value: 'group', label: 'Group' }]}
          />
        </div>
        {type === 'local' && (
          <div>
            <label style={{ fontSize: 12, fontWeight: 600, color: 'var(--holo-text-dim)', marginBottom: 4, display: 'block' }}>Path</label>
            <HoloInput value={path} onChange={e => { testReqSeq.current++; setTestResult(null); setPath(e.target.value) }} placeholder="./data/blobs/fast-ssd" />
          </div>
        )}
        {type === 's3' && (
          <>
            <div>
              <label style={{ fontSize: 12, fontWeight: 600, color: 'var(--holo-text-dim)', marginBottom: 4, display: 'block' }}>Bucket</label>
              <HoloInput value={bucket} onChange={e => { testReqSeq.current++; setTestResult(null); setBucket(e.target.value) }} />
            </div>
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10 }}>
              <div>
                <label style={{ fontSize: 12, fontWeight: 600, color: 'var(--holo-text-dim)', marginBottom: 4, display: 'block' }}>Region</label>
                <HoloInput value={region} onChange={e => { testReqSeq.current++; setTestResult(null); setRegion(e.target.value) }} />
              </div>
              <div>
                <label style={{ fontSize: 12, fontWeight: 600, color: 'var(--holo-text-dim)', marginBottom: 4, display: 'block' }}>Prefix (optional)</label>
                <HoloInput value={prefix} onChange={e => { testReqSeq.current++; setTestResult(null); setPrefix(e.target.value) }} />
              </div>
            </div>
            <div>
              <label style={{ fontSize: 12, fontWeight: 600, color: 'var(--holo-text-dim)', marginBottom: 4, display: 'block' }}>Endpoint (leave empty for AWS)</label>
              <HoloInput value={endpoint} onChange={e => { testReqSeq.current++; setTestResult(null); setEndpoint(e.target.value) }} placeholder="https://minio.example.com" />
            </div>
            <label style={{ display: 'flex', alignItems: 'flex-start', gap: 8, cursor: 'pointer', fontSize: 12, color: 'var(--holo-text)' }}>
              <input type="checkbox" checked={skipTLS}
                onChange={e => { testReqSeq.current++; setTestResult(null); setSkipTLS(e.target.checked) }}
                style={{ marginTop: 2 }} />
              <span>
                Skip TLS certificate verification
                <span style={{ display: 'block', color: 'var(--holo-text-faint)', fontSize: 11 }}>
                  For an endpoint behind a private CA. Credentials and every blob travel over an unverified connection.
                </span>
              </span>
            </label>
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10 }}>
              <div>
                <label style={{ fontSize: 12, fontWeight: 600, color: 'var(--holo-text-dim)', marginBottom: 4, display: 'block' }}>Access Key</label>
                <HoloInput value={accessKey} onChange={e => { testReqSeq.current++; setTestResult(null); setAccessKey(e.target.value) }} />
              </div>
              <div>
                <label style={{ fontSize: 12, fontWeight: 600, color: 'var(--holo-text-dim)', marginBottom: 4, display: 'block' }}>Secret Key</label>
                <HoloInput type="password" value={secretKey} onChange={e => { testReqSeq.current++; setTestResult(null); setSecretKey(e.target.value) }} />
              </div>
            </div>
          </>
        )}
        {type === 'group' && (
          <div>
            <div style={{ marginBottom: 12 }}>
              <label style={{ display: 'block', marginBottom: 6, color: '#94a3b8', fontSize: 13 }}>
                Fill Policy
              </label>
              <div style={{ display: 'flex', gap: 16 }}>
                {(['round_robin', 'write_to_first_fill'] as const).map(p => (
                  <label key={p} style={{ display: 'flex', alignItems: 'center', gap: 6, cursor: 'pointer', fontSize: 13, color: '#e2e8f0' }}>
                    <input type="radio" name="groupFillPolicy" value={p}
                      checked={groupFillPolicy === p}
                      onChange={() => setGroupFillPolicy(p)} />
                    {p === 'round_robin' ? 'Round Robin' : 'Write to First Fill'}
                  </label>
                ))}
              </div>
            </div>
            <div>
              <label style={{ display: 'block', marginBottom: 6, color: '#94a3b8', fontSize: 13 }}>
                Members (non-group stores)
              </label>
              <div style={{ display: 'flex', flexDirection: 'column', gap: 4, maxHeight: 160, overflowY: 'auto',
                background: 'rgba(255,255,255,0.04)', borderRadius: 8, padding: 8, border: '1px solid rgba(255,255,255,0.08)' }}>
                {blobStores.filter(s => s.type !== 'group').map(s => (
                  <label key={s.id} style={{ display: 'flex', alignItems: 'center', gap: 8, cursor: 'pointer', fontSize: 13, color: '#e2e8f0' }}>
                    <input type="checkbox"
                      checked={groupMemberIds.includes(s.id)}
                      onChange={e => setGroupMemberIds(prev =>
                        e.target.checked ? [...prev, s.id] : prev.filter(id => id !== s.id)
                      )} />
                    <span>{s.name}</span>
                    <span style={{ color: '#64748b', fontSize: 11 }}>{s.type}</span>
                  </label>
                ))}
                {blobStores.filter(s => s.type !== 'group').length === 0 && (
                  <span style={{ color: '#64748b', fontSize: 12 }}>No non-group stores available</span>
                )}
              </div>
            </div>
          </div>
        )}
        <div>
          <label style={{ fontSize: 12, fontWeight: 600, color: 'var(--holo-text-dim)', marginBottom: 4, display: 'block' }}>Quota (GB, optional)</label>
          <HoloInput type="number" min="0" step="0.1" value={quotaGB} onChange={e => setQuotaGB(e.target.value)} placeholder="Unlimited" />
        </div>
        {testResult && (
          <div style={{
            padding: '8px 12px', borderRadius: 8, fontSize: 13,
            background: testResult.ok ? 'rgba(34,197,94,0.08)' : 'rgba(239,68,68,0.08)',
            border: `1px solid ${testResult.ok ? 'rgba(34,197,94,0.3)' : 'rgba(239,68,68,0.3)'}`,
            color: testResult.ok ? 'var(--holo-green)' : 'var(--holo-red)',
          }}>
            {testResult.ok ? 'Connection successful' : `Connection failed: ${testResult.error}`}
          </div>
        )}
        {err && (
          <div role="alert" style={{ background: 'rgba(255,107,107,0.08)', border: '1px solid rgba(239,68,68,0.3)', borderRadius: 8, padding: '8px 12px', color: 'var(--holo-red)', fontSize: 13 }}>
            {err}
          </div>
        )}
        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8, marginTop: 4 }}>
          <HoloButton onClick={onClose}>Cancel</HoloButton>
          {type !== 'group' && (
            <HoloButton onClick={handleTest} disabled={testBusy || !name.trim() || (type === 's3' && !bucket.trim())}>
              {testBusy ? 'Testing…' : 'Test Connection'}
            </HoloButton>
          )}
          <HoloButton variant="primary" disabled={!name.trim() || mut.isPending} onClick={() => { setErr(''); mut.mutate() }}>
            {mut.isPending ? 'Creating…' : 'Create'}
          </HoloButton>
        </div>
      </div>
    </ModalShell>
  )
}

// ── Shared modal shell ────────────────────────────────────────────
function ModalShell({ title, onClose, width, children }: { title: string; onClose: () => void; width: number; children: React.ReactNode }) {
  return (
    <HoloModal open={true} onClose={onClose}>
      <div style={{ width, maxWidth: '100%' }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
          <h2 style={{ fontSize: 16, fontWeight: 700, color: 'var(--holo-text)', margin: 0 }}>{title}</h2>
          <HoloButton onClick={onClose} style={{ padding: 4 }}><X size={18} /></HoloButton>
        </div>
        {children}
      </div>
    </HoloModal>
  )
}

// ── Migration tab ─────────────────────────────────────────────────

interface MigrationJobData {
  id: string
  sourceUrl: string
  sourceUser: string
  status: 'pending' | 'running' | 'paused' | 'done' | 'error'
  migrateRepos: boolean
  migrateUsers: boolean
  migrateBlobs: boolean
  migratePolicies: boolean
  repositoriesTotal: number
  repositoriesDone: number
  assetsTotal: number
  assetsDone: number
  errorCount: number
  lastError?: string
  startedAt?: string
  finishedAt?: string
  createdAt: string
  updatedAt: string
}

const MIG_STATUS: Record<string, { bg: string; color: string }> = {
  pending:   { bg: 'rgba(245,158,11,0.15)',  color: '#f59e0b' },
  running:   { bg: 'rgba(59,130,246,0.15)',  color: '#3b82f6' },
  paused:    { bg: 'rgba(107,114,128,0.15)', color: '#9ca3af' },
  done:      { bg: 'rgba(34,197,94,0.15)',   color: '#22c55e' },
  error:     { bg: 'rgba(239,68,68,0.15)',   color: '#ef4444' },
}

function MigrationTab() {
  const qc = useQueryClient()
  const [showCreate, setShowCreate] = useState(false)

  const { data: jobs = [], isLoading, refetch } = useQuery<MigrationJobData[]>({
    queryKey: ['migrationJobs'],
    queryFn: () => nexspenceApi.listMigrationJobs().then(r => r.data),
    refetchInterval: (q) => {
      const list = q.state.data as MigrationJobData[] | undefined
      return list?.some(j => j.status === 'running') ? 3000 : false
    },
  })

  const pauseMut = useMutation({
    mutationFn: (id: string) => nexspenceApi.pauseMigrationJob(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['migrationJobs'] }),
  })
  const resumeMut = useMutation({
    mutationFn: (id: string) => nexspenceApi.resumeMigrationJob(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['migrationJobs'] }),
  })

  const activeJobs = jobs.filter(j => j.status === 'pending' || j.status === 'running' || j.status === 'paused')
  const historyJobs = jobs.filter(j => j.status === 'done' || j.status === 'error')

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 20 }}>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
          <ArrowRightLeft size={15} style={{ color: 'var(--holo-text-dim)' }} />
          <span style={{ fontSize: 15, fontWeight: 600, color: 'var(--holo-text)' }}>Migration from Nexus</span>
        </div>
        <div style={{ display: 'flex', gap: 8 }}>
          <HoloButton onClick={() => refetch()} title="Refresh"><RefreshCw size={14} /></HoloButton>
          <HoloButton variant="primary" onClick={() => setShowCreate(true)}><Plus size={14} /> New Migration</HoloButton>
        </div>
      </div>

      <div style={{ background: 'rgba(124,92,255,0.08)', border: '1px solid rgba(124,92,255,0.2)', borderRadius: 10, padding: '12px 16px', fontSize: 13, color: 'rgba(180,160,255,0.9)', lineHeight: 1.6 }}>
        <strong>How it works:</strong> Nexspence connects to your Nexus instance via its REST API and
        streams repositories, users, roles and all artifacts directly — no downtime required.
        Jobs are pausable and resumable. Requires Nexus admin credentials.
      </div>

      {isLoading ? (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
          <div className="holo-skeleton holo-skeleton--block" />
          <div className="holo-skeleton holo-skeleton--block" />
        </div>
      ) : activeJobs.length === 0 && historyJobs.length === 0 ? (
        <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: 12, color: 'var(--holo-text-faint)', fontSize: 14, padding: '48px 0' }}>
          <ArrowRightLeft size={40} style={{ opacity: 0.3 }} />
          <p style={{ margin: 0 }}>No migration jobs yet</p>
          <HoloButton variant="primary" onClick={() => setShowCreate(true)}><Plus size={14} /> Start Migration</HoloButton>
        </div>
      ) : (
        <>
          {activeJobs.length > 0 && (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
              {activeJobs.map(job => <MigrationJobCard key={job.id} job={job} onPause={() => pauseMut.mutate(job.id)} onResume={() => resumeMut.mutate(job.id)} />)}
            </div>
          )}

          {historyJobs.length > 0 && (
            <>
              <div style={{ fontSize: 12, fontWeight: 600, color: 'var(--holo-text-faint)', textTransform: 'uppercase', letterSpacing: '0.05em', marginTop: 4 }}>
                Migration History
              </div>
              <div style={{ background: 'rgba(255,255,255,0.02)', border: '1px solid var(--holo-border)', borderRadius: 12, overflow: 'hidden' }}>
                <div style={{ display: 'grid', gridTemplateColumns: '3fr 1fr 1fr 1fr 1fr', padding: '10px 16px', background: 'rgba(255,255,255,0.03)', borderBottom: '1px solid var(--holo-border)', fontSize: 11, fontWeight: 600, color: 'var(--holo-text-faint)', textTransform: 'uppercase', letterSpacing: '0.05em' }}>
                  <div>Source</div>
                  <div>Status</div>
                  <div>Repos</div>
                  <div>Assets</div>
                  <div>Finished</div>
                </div>
                {historyJobs.map(job => {
                  const st = MIG_STATUS[job.status] ?? MIG_STATUS.pending
                  return (
                    <div key={job.id} style={{ display: 'grid', gridTemplateColumns: '3fr 1fr 1fr 1fr 1fr', padding: '11px 16px', borderBottom: '1px solid rgba(255,255,255,0.04)', fontSize: 13, color: 'var(--holo-text)', alignItems: 'center' }}>
                      <div>
                        <Truncated text={job.sourceUrl} style={{ fontFamily: 'monospace', fontSize: 12, fontWeight: 600, color: 'var(--holo-text)' }} />
                        {job.sourceUser && <div style={{ fontSize: 11, color: 'var(--holo-text-faint)', marginTop: 2 }}>{job.sourceUser}</div>}
                      </div>
                      <div>
                        <span style={{ fontSize: 11, fontWeight: 600, padding: '2px 8px', borderRadius: 4, background: st.bg, color: st.color }}>{job.status}</span>
                        {job.errorCount > 0 && <div style={{ fontSize: 11, color: '#ef4444', marginTop: 3 }}>{job.errorCount} errors</div>}
                      </div>
                      <div style={{ fontSize: 13 }}>{job.repositoriesDone}/{job.repositoriesTotal || '?'}</div>
                      <div style={{ fontSize: 13 }}>{job.assetsDone.toLocaleString()}/{job.assetsTotal ? job.assetsTotal.toLocaleString() : '?'}</div>
                      <div style={{ fontSize: 12, color: 'var(--holo-text-faint)' }}>
                        {job.finishedAt ? new Date(job.finishedAt).toLocaleString() : job.updatedAt ? new Date(job.updatedAt).toLocaleString() : '—'}
                      </div>
                    </div>
                  )
                })}
              </div>
            </>
          )}
        </>
      )}

      {showCreate && (
        <CreateMigrationJobModal
          onClose={() => setShowCreate(false)}
          onCreated={() => {
            setShowCreate(false)
            qc.invalidateQueries({ queryKey: ['migrationJobs'] })
          }}
        />
      )}
    </div>
  )
}

function MigrationJobCard({ job, onPause, onResume }: { job: MigrationJobData; onPause: () => void; onResume: () => void }) {
  const reposPct = job.repositoriesTotal ? Math.round((job.repositoriesDone / job.repositoriesTotal) * 100) : 0
  const assetsPct = job.assetsTotal ? Math.round((job.assetsDone / job.assetsTotal) * 100) : 0
  const st = MIG_STATUS[job.status] ?? MIG_STATUS.pending
  return (
    <HoloCard style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
        <ArrowRightLeft size={15} style={{ color: 'var(--holo-text-faint)', flexShrink: 0 }} />
        <span style={{ flex: 1, fontSize: 14, fontWeight: 600, color: 'var(--holo-text)', fontFamily: 'monospace', wordBreak: 'break-all' }}>{job.sourceUrl}</span>
        <span style={{ fontSize: 11, fontWeight: 600, padding: '3px 9px', borderRadius: 4, background: st.bg, color: st.color }}>{job.status}</span>
      </div>

      <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' as const }}>
        {[
          { label: 'Repos', on: job.migrateRepos },
          { label: 'Users', on: job.migrateUsers },
          { label: 'Policies', on: job.migratePolicies },
          { label: 'Artifacts', on: job.migrateBlobs },
        ].map(s => (
          <span key={s.label} style={{ fontSize: 11, padding: '2px 8px', borderRadius: 4, background: s.on ? 'rgba(59,130,246,0.15)' : 'rgba(255,255,255,0.04)', color: s.on ? '#3b82f6' : 'var(--holo-text-faint)', fontWeight: 600 }}>{s.label}</span>
        ))}
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: 12 }}>
        <div style={{ background: 'rgba(255,255,255,0.02)', borderRadius: 8, padding: '10px 12px' }}>
          <div style={{ fontSize: 11, color: 'var(--holo-text-faint)', marginBottom: 4, textTransform: 'uppercase', letterSpacing: '0.04em' }}>Repositories</div>
          <div style={{ fontSize: 18, fontWeight: 700, color: 'var(--holo-text)' }}>{job.repositoriesDone}<span style={{ fontSize: 13, color: 'var(--holo-text-faint)', fontWeight: 400 }}>/{job.repositoriesTotal || '?'}</span></div>
          <div style={{ height: 4, borderRadius: 2, background: 'rgba(255,255,255,0.08)', overflow: 'hidden', marginTop: 6 }}>
            <div style={{ height: '100%', width: reposPct + '%', background: 'var(--holo-a)', transition: 'width 0.4s' }} />
          </div>
        </div>
        <div style={{ background: 'rgba(255,255,255,0.02)', borderRadius: 8, padding: '10px 12px' }}>
          <div style={{ fontSize: 11, color: 'var(--holo-text-faint)', marginBottom: 4, textTransform: 'uppercase', letterSpacing: '0.04em' }}>Assets</div>
          <div style={{ fontSize: 18, fontWeight: 700, color: 'var(--holo-text)' }}>{job.assetsDone}<span style={{ fontSize: 13, color: 'var(--holo-text-faint)', fontWeight: 400 }}>/{job.assetsTotal || '?'}</span></div>
          <div style={{ height: 4, borderRadius: 2, background: 'rgba(255,255,255,0.08)', overflow: 'hidden', marginTop: 6 }}>
            <div style={{ height: '100%', width: assetsPct + '%', background: '#22c55e', transition: 'width 0.4s' }} />
          </div>
        </div>
        <div style={{ background: 'rgba(255,255,255,0.02)', borderRadius: 8, padding: '10px 12px' }}>
          <div style={{ fontSize: 11, color: 'var(--holo-text-faint)', marginBottom: 4, textTransform: 'uppercase', letterSpacing: '0.04em' }}>Errors</div>
          <div style={{ fontSize: 18, fontWeight: 700, color: job.errorCount > 0 ? '#ef4444' : '#22c55e' }}>{job.errorCount}</div>
        </div>
      </div>

      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
        <span style={{ fontSize: 12, color: 'var(--holo-text-faint)' }}>Started {job.startedAt ? new Date(job.startedAt).toLocaleString() : new Date(job.createdAt).toLocaleString()}</span>
        <div style={{ display: 'flex', gap: 8 }}>
          {job.status === 'running' && <HoloButton onClick={onPause}><Pause size={12} /> Pause</HoloButton>}
          {job.status === 'paused' && <HoloButton onClick={onResume}><Play size={12} /> Resume</HoloButton>}
        </div>
      </div>
    </HoloCard>
  )
}

function CreateMigrationJobModal({ onClose, onCreated }: { onClose: () => void; onCreated: () => void }) {
  const [form, setForm] = useState({
    sourceUrl: '', username: 'admin', password: '',
  })
  const [scope, setScope] = useState({
    migrateRepos: true, migrateUsers: true, migratePolicies: true, migrateBlobs: true,
  })
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  const set = (k: keyof typeof form) => (e: React.ChangeEvent<HTMLInputElement>) =>
    setForm(f => ({ ...f, [k]: e.target.value }))

  const toggleScope = (k: keyof typeof scope) =>
    setScope(s => ({ ...s, [k]: !s[k] }))

  const validateStep = (stepIdx: number): boolean => {
    setError('')
    if (stepIdx === 0) {
      if (!form.sourceUrl.trim()) { setError('Nexus URL is required'); return false }
      if (!form.password.trim()) { setError('Password is required'); return false }
    }
    if (stepIdx === 1) {
      if (!Object.values(scope).some(Boolean)) { setError('Select at least one scope item'); return false }
    }
    return true
  }

  const handleFinish = async () => {
    setError('')
    setLoading(true)
    try {
      await nexspenceApi.createMigrationJob({
        sourceUrl: form.sourceUrl,
        credentials: { username: form.username, password: form.password },
        scope: {
          migrateRepos: scope.migrateRepos,
          migrateUsers: scope.migrateUsers,
          migratePolicies: scope.migratePolicies,
          migrateBlobs: scope.migrateBlobs,
        },
      })
      onCreated()
    } catch (err: unknown) {
      const e = err as { response?: { data?: { error?: string } } }
      setError(e.response?.data?.error ?? 'Failed to create migration job')
    } finally {
      setLoading(false)
    }
  }

  const LABEL = { fontSize: 11, fontWeight: 600 as const, color: 'var(--holo-text-dim)', textTransform: 'uppercase' as const, letterSpacing: '0.04em' }

  const scopeItems: { key: keyof typeof scope; label: string }[] = [
    { key: 'migrateRepos',    label: 'Repositories' },
    { key: 'migrateUsers',    label: 'Users & Roles' },
    { key: 'migratePolicies', label: 'Cleanup Policies' },
    { key: 'migrateBlobs',   label: 'Artifacts (blobs)' },
  ]

  const step1 = (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 5 }}>
        <label style={LABEL}>Nexus URL *</label>
        <HoloInput placeholder="https://nexus.example.com" value={form.sourceUrl} onChange={set('sourceUrl')} autoFocus />
      </div>
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 5 }}>
          <label style={LABEL}>Username</label>
          <HoloInput value={form.username} onChange={set('username')} />
        </div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 5 }}>
          <label style={LABEL}>Password *</label>
          <HoloInput type="password" value={form.password} onChange={set('password')} />
        </div>
      </div>
    </div>
  )

  const step2 = (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
      <label style={LABEL}>Migration Scope</label>
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 8 }}>
        {scopeItems.map(({ key, label }) => (
          <label key={key} style={{
            display: 'flex', alignItems: 'center', gap: 8, cursor: 'pointer',
            padding: '8px 10px',
            background: scope[key] ? 'rgba(59,130,246,0.1)' : 'rgba(255,255,255,0.03)',
            border: `1px solid ${scope[key] ? 'rgba(59,130,246,0.3)' : 'rgba(255,255,255,0.08)'}`,
            borderRadius: 8, transition: 'background 0.15s, border-color 0.15s', userSelect: 'none',
          }}>
            <input type="checkbox" checked={scope[key]} onChange={() => toggleScope(key)} style={{ accentColor: '#3b82f6', width: 14, height: 14 }} />
            <span style={{ fontSize: 13, color: scope[key] ? 'var(--holo-text)' : 'var(--holo-text-faint)', fontWeight: scope[key] ? 600 : 400 }}>{label}</span>
          </label>
        ))}
      </div>
    </div>
  )

  const step3 = (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
      <div style={{
        background: 'rgba(255,255,255,0.03)', border: '1px solid rgba(124,92,255,0.15)',
        borderRadius: 10, padding: '12px 14px', display: 'flex', flexDirection: 'column', gap: 6,
      }}>
        <div style={{ fontSize: 11, fontWeight: 700, color: '#7c5cff', textTransform: 'uppercase', letterSpacing: '0.06em', marginBottom: 4 }}>Summary</div>
        <div style={{ fontSize: 12, color: 'var(--holo-text-dim)' }}>
          <b style={{ color: 'var(--holo-text)' }}>Source:</b> {form.sourceUrl || '—'}
        </div>
        <div style={{ fontSize: 12, color: 'var(--holo-text-dim)' }}>
          <b style={{ color: 'var(--holo-text)' }}>Scope:</b>{' '}
          {scopeItems.filter(i => scope[i.key]).map(i => i.label).join(', ') || 'none'}
        </div>
      </div>
    </div>
  )

  return (
    <Wizard
      steps={[
        { label: 'Source', content: step1 },
        { label: 'Scope', content: step2 },
        { label: 'Options', content: step3 },
      ]}
      onFinish={handleFinish}
      finishLabel="Start Migration"
      onValidateStep={validateStep}
      onClose={onClose}
      loading={loading}
      error={error}
    />
  )
}
