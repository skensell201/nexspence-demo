import React, { useState, useEffect, useMemo, useRef } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import axios from 'axios'
import { Shield, RefreshCw, Webhook, AlertTriangle, CheckCircle, Loader, Trash2, Plus, Bug, Zap, Pencil, Download } from 'lucide-react'
import { nexusApi, nexspenceApi, apiClient, apiErrorMessage, type ScannerStatus } from '@/api/client'
import { saveBlob, exportFilename } from '@/api/download'
import { UsersTab } from './UsersPage'
import { useAuthStore } from '@/store/authStore'
import { Select } from '../components/Select'
import { HoloButton, HoloInput, HoloModal, HoloTabs, HoloPill, HoloCard, HoloTabItem } from '@/components/holo'
import { Truncated } from '@/components/Truncated'

/* ─── Types ─────────────────────────────────────────────── */
interface Role { id: string; name: string; description: string; privileges: string[]; roles: string[]; readOnly: boolean; source?: string }
interface CVEFinding { id: string; severity: string; pkgName: string; installedVersion: string; fixedVersion?: string; title?: string }
interface ScanSummary { malicious: number; critical: number; high: number; medium: number; low: number; unknown: number; total: number }
interface ScanResult { scannedAt: string; imageRef: string; status: string; error?: string; summary: ScanSummary; findings: CVEFinding[] }

interface SecuritySummary {
  malicious: number
  critical: number
  high: number
  medium: number
  low: number
  unknown: number
  scanned_total: number
}

interface VulnRow {
  repoName: string
  format: string
  componentId: string
  name: string
  version: string
  malicious: number
  critical: number
  high: number
  medium: number
  low: number
  unknown: number
  scannedAt: string
}

/* ─── Access Map types ───────────────────────────────── */
interface GraphUser     { id:string; username:string; email:string; status:string; source:string; roleIds:string[] }
interface GraphRole     { id:string; name:string; description:string; privilegeIds:string[]; roleIds:string[] }
interface GraphPrivilege{ id:string; name:string; type:string; attrs?:Record<string,unknown>; contentSelectorId?:string }
interface GraphSelector { id:string; name:string; expression:string }
interface AccessGraph   { users:GraphUser[]; roles:GraphRole[]; privileges:GraphPrivilege[]; selectors:GraphSelector[] }
type GNodeType = 'user' | 'role' | 'privilege' | 'selector'

const NODE_W = 120, NODE_H = 26, ROW_GAP = 40, ROW_Y0 = 36, SVG_W = 680
const COL_CX: Record<GNodeType, number> = { user: 70, role: 230, privilege: 390, selector: 560 }
const NODE_COLORS: Record<GNodeType, {stroke:string;fill:string;text:string}> = {
  user:      { stroke:'#7c5cff', fill:'#1e1049', text:'#c4b5fd' },
  role:      { stroke:'#3b82f6', fill:'#0c2340', text:'#93c5fd' },
  privilege: { stroke:'#f59e0b', fill:'#1c1200', text:'#fde68a' },
  selector:  { stroke:'#22c55e', fill:'#001c0c', text:'#86efac' },
}

interface WebhookDef { id: string; name: string; url: string; events: string[]; active: boolean; secret?: string }
interface Privilege {
  id: string
  name: string
  description: string
  type: 'wildcard' | 'repository-view' | 'repository-admin' | 'application' | 'script'
  attrs: Record<string, unknown>
  contentSelectorId?: string
  readOnly: boolean
}

const PRIV_TYPE_COLOR: Record<string, string> = {
  'wildcard': '#3b82f6',
  'repository-view': '#22c55e',
  'repository-admin': '#f59e0b',
  'application': '#a78bfa',
  'script': '#f97316',
  'repository-content-selector': '#06b6d4',
}

// MALICIOUS is deliberately off the red→green CVSS ramp: a compromised package
// is a different class of alert, not a worse CVE, and reading it as "extra red"
// is exactly the confusion to avoid.
const sevColor = (s: string) => s === 'MALICIOUS' ? '#d946ef' : s === 'CRITICAL' ? '#ef4444' : s === 'HIGH' ? '#f97316' : s === 'MEDIUM' ? '#f59e0b' : s === 'LOW' ? '#22c55e' : '#6b7280'
const monoStyle = { fontFamily: 'monospace' as const, fontSize: 12 }
const emptyStyle = { textAlign: 'center' as const, color: 'var(--holo-text-faint)', fontSize: 14, padding: 32 }

/* ─── Sub-components ────────────────────────────────────── */

function RoleModal({
  title,
  form,
  onFormChange,
  allPrivs,
  selectedPrivIds,
  onPrivToggle,
  loadingPrivs,
  onSave,
  saving,
  saveDisabled,
  onCancel,
  onDelete,
  error,
}: {
  title: string
  form: { name: string; description: string }
  onFormChange: (f: { name: string; description: string }) => void
  allPrivs: Privilege[]
  selectedPrivIds: string[]
  onPrivToggle: (ids: string[]) => void
  loadingPrivs: boolean
  onSave: () => void
  saving: boolean
  saveDisabled: boolean
  onCancel: () => void
  onDelete?: () => void
  error?: string | null
}) {
  return (
    <HoloModal open={true} onClose={onCancel} style={{ minWidth: 640 }}>
      <h3 style={{ margin: 0, fontSize: 16, fontWeight: 700, color: 'var(--holo-text)' }}>{title}</h3>
      <HoloInput placeholder="Name *" value={form.name} onChange={e => onFormChange({ ...form, name: e.target.value })} />
      <HoloInput placeholder="Description (optional)" value={form.description} onChange={e => onFormChange({ ...form, description: e.target.value })} />

      <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--holo-text-dim)', marginTop: 4 }}>Privileges</div>
      {loadingPrivs ? (
        <div style={emptyStyle}>Loading privileges…</div>
      ) : (
        <PrivilegeTransferList
          allPrivs={allPrivs}
          selectedIds={selectedPrivIds}
          onChange={onPrivToggle}
        />
      )}

      {error && (
        <div role="alert" style={{ padding: '8px 12px', background: 'rgba(239,68,68,0.1)', border: '1px solid rgba(239,68,68,0.3)', borderRadius: 8, color: 'var(--holo-red)', fontSize: 12 }}>{error}</div>
      )}

      <div style={{ display: 'flex', gap: 8, justifyContent: 'space-between', marginTop: 4 }}>
        <div>{onDelete && <HoloButton variant="danger" onClick={onDelete}>Delete</HoloButton>}</div>
        <div style={{ display: 'flex', gap: 8 }}>
          <HoloButton onClick={onCancel}>Cancel</HoloButton>
          <HoloButton variant="primary" onClick={onSave} disabled={saving || saveDisabled}>
            {saving ? 'Saving…' : 'Save'}
          </HoloButton>
        </div>
      </div>
    </HoloModal>
  )
}

function PrivilegeTransferList({
  allPrivs, selectedIds, onChange,
}: {
  allPrivs: Privilege[]
  selectedIds: string[]
  onChange: (ids: string[]) => void
}) {
  const [leftSearch, setLeftSearch] = useState('')
  const [rightSearch, setRightSearch] = useState('')

  const available = allPrivs.filter(p =>
    !selectedIds.includes(p.id) &&
    (!leftSearch || p.name.toLowerCase().includes(leftSearch.toLowerCase()))
  )
  const selected = allPrivs.filter(p =>
    selectedIds.includes(p.id) &&
    (!rightSearch || p.name.toLowerCase().includes(rightSearch.toLowerCase()))
  )

  function add(id: string) { onChange([...selectedIds, id]) }
  function remove(id: string) { onChange(selectedIds.filter(x => x !== id)) }
  function addAll() { onChange([...new Set([...selectedIds, ...available.map(p => p.id)])]) }
  function removeAll() { onChange([]) }

  const panelStyle: React.CSSProperties = {
    border: '1px solid rgba(124,92,255,0.2)', borderRadius: 10, overflow: 'hidden', flex: 1,
  }
  const headerStyle: React.CSSProperties = {
    padding: '6px 10px', fontSize: 11, fontWeight: 600, color: 'var(--holo-text-dim)',
    textTransform: 'uppercase' as const, letterSpacing: '0.4px',
    borderBottom: '1px solid rgba(255,255,255,0.06)', background: 'rgba(0,0,0,0.2)',
  }
  const listStyle: React.CSSProperties = { maxHeight: 160, overflowY: 'auto' as const }
  const itemBase: React.CSSProperties = {
    padding: '6px 10px', fontSize: 12, cursor: 'pointer',
    borderBottom: '1px solid rgba(255,255,255,0.03)',
  }
  const arrowBtn: React.CSSProperties = {
    width: 28, height: 28, display: 'flex', alignItems: 'center', justifyContent: 'center',
    borderRadius: 8, border: '1px solid rgba(124,92,255,0.2)',
    background: 'rgba(124,92,255,0.1)', color: 'var(--holo-a)', cursor: 'pointer', fontSize: 14,
  }

  return (
    <div style={{ display: 'grid', gridTemplateColumns: '1fr 28px 1fr', gap: 8, alignItems: 'stretch' }}>
      <div style={panelStyle}>
        <div style={headerStyle}>Available ({available.length})</div>
        <div style={{ padding: '4px 6px', borderBottom: '1px solid rgba(255,255,255,0.05)' }}>
          <input
            placeholder="Filter…"
            value={leftSearch}
            onChange={e => setLeftSearch(e.target.value)}
            className="holo-input"
            style={{ width: '100%', boxSizing: 'border-box' as const, fontSize: 11, padding: '4px 8px' }}
          />
        </div>
        <div style={listStyle}>
          {available.map(p => (
            <div key={p.id} style={{ ...itemBase, color: 'var(--holo-text)' }}
              onClick={() => add(p.id)}
              onMouseEnter={e => (e.currentTarget as HTMLDivElement).style.background = 'rgba(124,92,255,0.08)'}
              onMouseLeave={e => (e.currentTarget as HTMLDivElement).style.background = 'transparent'}
            >{p.name}</div>
          ))}
          {available.length === 0 && (
            <div style={{ ...itemBase, color: 'var(--holo-text-faint)' }}>
              {leftSearch ? 'No matches' : 'All selected'}
            </div>
          )}
        </div>
      </div>

      <div style={{ display: 'flex', flexDirection: 'column', gap: 6, justifyContent: 'center', alignItems: 'center' }}>
        <button type="button" style={arrowBtn} onClick={addAll} title="Add all">→</button>
        <button type="button" style={arrowBtn} onClick={removeAll} title="Remove all">←</button>
      </div>

      <div style={panelStyle}>
        <div style={headerStyle}>Selected ({selected.length})</div>
        <div style={{ padding: '4px 6px', borderBottom: '1px solid rgba(255,255,255,0.05)' }}>
          <input
            placeholder="Filter…"
            value={rightSearch}
            onChange={e => setRightSearch(e.target.value)}
            className="holo-input"
            style={{ width: '100%', boxSizing: 'border-box' as const, fontSize: 11, padding: '4px 8px' }}
          />
        </div>
        <div style={listStyle}>
          {selected.map(p => (
            <div key={p.id} style={{ ...itemBase, color: '#c4b5fd', background: 'rgba(124,92,255,0.12)', display: 'flex', alignItems: 'center', gap: 6 }}
              onClick={() => remove(p.id)}
              onMouseEnter={e => (e.currentTarget as HTMLDivElement).style.background = 'rgba(124,92,255,0.2)'}
              onMouseLeave={e => (e.currentTarget as HTMLDivElement).style.background = 'rgba(124,92,255,0.12)'}
            >
              <span style={{ width: 6, height: 6, borderRadius: '50%', background: '#7c5cff', flexShrink: 0, display: 'inline-block' }} />
              {p.name}
            </div>
          ))}
          {selected.length === 0 && (
            <div style={{ ...itemBase, color: 'var(--holo-text-faint)' }}>None selected</div>
          )}
        </div>
      </div>
    </div>
  )
}

function RolesTab({ roles, loading, onRefresh, admin }: { roles: Role[]; loading: boolean; onRefresh: () => void; admin: boolean }) {
  const qc = useQueryClient()

  const [editRole, setEditRole] = useState<Role | null>(null)
  const [editForm, setEditForm] = useState({ name: '', description: '' })
  const [editPrivIds, setEditPrivIds] = useState<string[]>([])

  const [showCreate, setShowCreate] = useState(false)
  const [createForm, setCreateForm] = useState({ name: '', description: '' })
  const [createPrivIds, setCreatePrivIds] = useState<string[]>([])

  const [allPrivs, setAllPrivs] = useState<Privilege[]>([])
  const [loadingPrivs, setLoadingPrivs] = useState(false)

  const [editError, setEditError] = useState<string | null>(null)
  const [createError, setCreateError] = useState<string | null>(null)

  const [roleSearch, setRoleSearch] = useState('')
  const [expandedRoles, setExpandedRoles] = useState<Set<string>>(new Set())
  const [rolePrivCache, setRolePrivCache] = useState<Map<string, Privilege[]>>(new Map())
  const [loadingExpand, setLoadingExpand] = useState<Set<string>>(new Set())

  // Responses are applied in resolution order, not request order: opening
  // Edit on role A, then closing/opening Edit on role B before A's slower
  // request resolves, otherwise lets A's late response land after B's fast
  // one and silently overwrite the checklist the admin is now looking at for
  // B — the same bug class #337 fixed via vulnReqSeq further down this file.
  const editReqSeq = useRef(0)

  const { data: allSelectors = [] } = useQuery<{ id: string; name: string }[]>({
    queryKey: ['content-selectors'],
    queryFn: () => nexusApi.listContentSelectors().then(r => r.data),
    staleTime: 60_000,
  })

  const filtered = roles.filter(r =>
    r.name.toLowerCase().includes(roleSearch.toLowerCase())
  )

  async function toggleExpand(roleId: string) {
    setExpandedRoles(prev => {
      const next = new Set(prev)
      if (next.has(roleId)) { next.delete(roleId) } else { next.add(roleId) }
      return next
    })
    if (!rolePrivCache.has(roleId)) {
      setLoadingExpand(prev => new Set(prev).add(roleId))
      try {
        const privs = await nexusApi.listRolePrivileges(roleId).then(r => r.data as Privilege[])
        setRolePrivCache(prev => new Map(prev).set(roleId, privs))
      } finally {
        setLoadingExpand(prev => { const next = new Set(prev); next.delete(roleId); return next })
      }
    }
  }


  async function loadPrivs() {
    setLoadingPrivs(true)
    try {
      setAllPrivs(await nexusApi.listPrivileges().then(r => r.data as Privilege[]))
    } finally { setLoadingPrivs(false) }
  }

  async function openEdit(r: Role) {
    const seq = ++editReqSeq.current
    setEditRole(r)
    setEditForm({ name: r.name, description: r.description })
    setLoadingPrivs(true)
    try {
      const [privList, rolePrivs] = await Promise.all([
        nexusApi.listPrivileges().then(res => res.data as Privilege[]),
        nexusApi.listRolePrivileges(r.id).then(res => res.data as Privilege[]),
      ])
      if (seq !== editReqSeq.current) return // superseded by a newer edit
      setAllPrivs(privList)
      setEditPrivIds(rolePrivs.map(p => p.id))
    } finally {
      if (seq === editReqSeq.current) setLoadingPrivs(false)
    }
  }

  async function openCreate() {
    setCreateForm({ name: '', description: '' })
    setCreatePrivIds([])
    setCreateError(null)
    setShowCreate(true)
    await loadPrivs()
  }

  const saveEdit = useMutation({
    mutationFn: async () => {
      if (!editRole) return
      await nexusApi.updateRole(editRole.id, editForm)
      await nexusApi.setRolePrivileges(editRole.id, editPrivIds)
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['roles'] })
      onRefresh()
      if (editRole) setRolePrivCache(prev => { const next = new Map(prev); next.delete(editRole.id); return next })
      setEditRole(null)
    },
    onError: (e: unknown) => {
      let msg = 'Error saving role'
      if (axios.isAxiosError(e)) {
        const d = e.response?.data
        if (typeof d === 'object' && d !== null && 'error' in d) msg = String((d as { error: unknown }).error)
        else msg = e.message
      } else if (e instanceof Error) { msg = e.message }
      setEditError(msg)
    },
  })

  const create = useMutation({
    mutationFn: async () => {
      const res = await apiClient.post<Role>('/service/rest/v1/security/roles', createForm)
      if (createPrivIds.length > 0) await nexusApi.setRolePrivileges(res.data.id, createPrivIds)
    },
    onSuccess: () => { qc.invalidateQueries({ queryKey: ['roles'] }); onRefresh(); setShowCreate(false) },
    onError: (e: unknown) => {
      let msg = 'Error creating role'
      if (axios.isAxiosError(e)) {
        const d = e.response?.data
        if (typeof d === 'object' && d !== null && 'error' in d) msg = String((d as { error: unknown }).error)
        else msg = e.message
      } else if (e instanceof Error) { msg = e.message }
      setCreateError(msg)
    },
  })

  const del = useMutation({
    mutationFn: (id: string) => nexusApi.deleteRole(id),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ['roles'] }); onRefresh() },
    onError: (e: unknown) => {
      let msg = 'Error deleting role'
      if (axios.isAxiosError(e)) {
        const d = e.response?.data
        if (typeof d === 'object' && d !== null && 'error' in d) msg = String((d as { error: unknown }).error)
        else msg = e.message
      } else if (e instanceof Error) { msg = e.message }
      setEditError(msg)
    },
  })

  if (loading) return <div style={emptyStyle}>Loading…</div>

  return (
    <>
      {admin && (
        <div style={{ display: 'flex', justifyContent: 'flex-end', marginBottom: 8 }}>
          <HoloButton variant="primary" icon={<Plus size={14} />} onClick={openCreate}>New Role</HoloButton>
        </div>
      )}

      <HoloInput
        style={{ marginBottom: 12 }}
        placeholder="Search roles…"
        value={roleSearch}
        onChange={e => setRoleSearch(e.target.value)}
      />
      {!filtered.length ? <div style={emptyStyle}>No roles found</div> : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
          {filtered.map(r => (
            <div key={r.id} style={{
              display: 'grid', gridTemplateColumns: '20px 1fr auto',
              alignItems: 'center', gap: 12, padding: '11px 16px',
              background: 'rgba(10,8,28,0.97)', border: '1px solid rgba(124,92,255,0.2)',
              borderRadius: 10, transition: 'border-color 0.15s, background 0.15s',
            }}
            onMouseEnter={e => { (e.currentTarget as HTMLDivElement).style.borderColor = 'rgba(124,92,255,0.45)'; (e.currentTarget as HTMLDivElement).style.background = 'rgba(124,92,255,0.04)' }}
            onMouseLeave={e => { (e.currentTarget as HTMLDivElement).style.borderColor = 'rgba(124,92,255,0.2)'; (e.currentTarget as HTMLDivElement).style.background = 'rgba(10,8,28,0.97)' }}
            >
              <Shield size={15} style={{ color: 'var(--holo-a)', flexShrink: 0 }} />
              <div style={{ minWidth: 0 }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 6, flexWrap: 'wrap' as const }}>
                  <span style={{ fontSize: 13, fontWeight: 600, color: 'var(--holo-text)' }}>{r.name}</span>
                  {r.readOnly && <HoloPill style={{ fontSize: 11 }}>built-in</HoloPill>}
                </div>
                {r.description && <div style={{ fontSize: 11, color: 'var(--holo-text-faint)', marginTop: 1 }}>{r.description}</div>}
                {(r.privileges ?? []).length > 0 && (
                  <button
                    type="button"
                    onClick={e => { e.stopPropagation(); void toggleExpand(r.id) }}
                    style={{
                      marginTop: 4, display: 'inline-flex', alignItems: 'center', gap: 5,
                      background: 'rgba(99,102,241,0.12)', border: '1px solid rgba(99,102,241,0.25)',
                      borderRadius: 6, padding: '2px 8px', cursor: 'pointer', fontSize: 11,
                      color: '#a5b4fc', transition: 'background 0.15s',
                    }}
                    onMouseEnter={e => (e.currentTarget.style.background = 'rgba(99,102,241,0.22)')}
                    onMouseLeave={e => (e.currentTarget.style.background = 'rgba(99,102,241,0.12)')}
                  >
                    {loadingExpand.has(r.id)
                      ? <Loader size={11} style={{ animation: 'spin 1s linear infinite' }} />
                      : <span style={{ fontSize: 10 }}>{expandedRoles.has(r.id) ? '▲' : '▼'}</span>
                    }
                    {(r.privileges ?? []).length} privilege{(r.privileges ?? []).length !== 1 ? 's' : ''}
                  </button>
                )}
                {expandedRoles.has(r.id) && (
                  <div style={{
                    marginTop: 8, borderTop: '1px solid rgba(124,92,255,0.15)',
                    paddingTop: 8, display: 'flex', flexDirection: 'column', gap: 4,
                  }}>
                    {(rolePrivCache.get(r.id) ?? []).length === 0 && !loadingExpand.has(r.id) && (
                      <span style={{ fontSize: 11, color: 'var(--holo-text-faint)' }}>No privileges assigned</span>
                    )}
                    {(rolePrivCache.get(r.id) ?? []).map(p => {
                      const actions = (p.attrs?.actions as string[] | undefined) ?? []
                      const typeColor = PRIV_TYPE_COLOR[p.type] ?? '#6b7280'
                      const csName = allSelectors.find(s => s.id === p.contentSelectorId)?.name
                      return (
                        <div key={p.id} style={{
                          display: 'flex', alignItems: 'center', gap: 6, flexWrap: 'wrap' as const,
                          padding: '4px 0',
                        }}>
                          <span style={{
                            fontSize: 9, fontWeight: 700, padding: '1px 5px', borderRadius: 3,
                            textTransform: 'uppercase' as const, letterSpacing: '0.4px',
                            background: typeColor + '22', color: typeColor, whiteSpace: 'nowrap' as const,
                          }}>
                            {(p.type as string) === 'repository-content-selector' ? 'cs' : p.type.replace('repository-', '')}
                          </span>
                          <span style={{ fontSize: 12, color: 'var(--holo-text)', fontWeight: 500 }}>{p.name}</span>
                          {csName && (
                            <span style={{ fontSize: 10, color: '#67e8f9', padding: '1px 5px', background: 'rgba(6,182,212,0.1)', borderRadius: 3 }}>
                              {csName}
                            </span>
                          )}
                          {actions.map(a => {
                            const ac = (a === 'write' || a === 'delete') ? '#f59e0b' : '#22c55e'
                            return (
                              <span key={a} style={{ fontSize: 9, padding: '1px 5px', borderRadius: 3, background: ac + '22', color: ac }}>
                                {a}
                              </span>
                            )
                          })}
                        </div>
                      )
                    })}
                  </div>
                )}
              </div>
              <div style={{ display: 'flex', gap: 6 }}>
                {!r.readOnly && admin && (
                  <HoloButton style={{ padding: '4px 10px', fontSize: 12 }} onClick={() => openEdit(r)}>Edit</HoloButton>
                )}
              </div>
            </div>
          ))}
        </div>
      )}

      {editRole && (
        <RoleModal
          title={`Edit Role: ${editRole.name}`}
          form={editForm}
          onFormChange={setEditForm}
          allPrivs={allPrivs}
          selectedPrivIds={editPrivIds}
          onPrivToggle={setEditPrivIds}
          loadingPrivs={loadingPrivs}
          onSave={() => saveEdit.mutate()}
          saving={saveEdit.isPending}
          saveDisabled={!editForm.name.trim()}
          onCancel={() => { editReqSeq.current++; setEditRole(null); setEditError(null) }}
          onDelete={() => { if (confirm(`Delete role ${editRole.name}?`)) { del.mutate(editRole.id); setEditRole(null) } }}
          error={editError}
        />
      )}

      {showCreate && (
        <RoleModal
          title="New Role"
          form={createForm}
          onFormChange={setCreateForm}
          allPrivs={allPrivs}
          selectedPrivIds={createPrivIds}
          onPrivToggle={setCreatePrivIds}
          loadingPrivs={loadingPrivs}
          onSave={() => create.mutate()}
          saving={create.isPending}
          saveDisabled={!createForm.name.trim()}
          onCancel={() => { setShowCreate(false); setCreateError(null) }}
          error={createError}
        />
      )}
    </>
  )
}

const SEVERITY_ORDER = ['MALICIOUS', 'CRITICAL', 'HIGH', 'MEDIUM', 'LOW', 'UNKNOWN']

function fmtElapsedSec(s: number): string {
  const m = Math.floor(s / 60)
  return m > 0 ? `${m}m ${s % 60}s` : `${s}s`
}

function ScanTab() {
  const [componentId, setComponentId] = useState('')
  const [imageRef, setImageRef] = useState('')
  const [result, setResult] = useState<ScanResult | null>(null)
  const [scanning, setScanning] = useState(false)
  const [error, setError] = useState('')
  const [severityFilter, setSeverityFilter] = useState('ALL')
  const [elapsed, setElapsed] = useState(0)
  const [scanner, setScanner] = useState<ScannerStatus | null>(null)

  useEffect(() => {
    nexspenceApi.getScannerStatus()
      .then(r => setScanner(r.data))
      .catch(() => setScanner(null))
  }, [])

  useEffect(() => {
    if (!scanning) { setElapsed(0); return }
    const t = setInterval(() => setElapsed((n) => n + 1), 1000)
    return () => clearInterval(t)
  }, [scanning])

  async function runScan() {
    if (!componentId.trim()) { setError('Enter a component ID'); return }
    setScanning(true); setError(''); setResult(null)
    try {
      const res = await apiClient.post<ScanResult>(`/api/v1/components/${componentId.trim()}/scan`, { imageRef: imageRef.trim() || undefined })
      setResult(res.data)
    } catch (e: unknown) {
      let msg: string
      if (axios.isAxiosError(e)) {
        const d = e.response?.data
        const fromBody = typeof d === 'object' && d !== null && 'error' in d && typeof (d as { error?: string }).error === 'string'
          ? (d as { error: string }).error
          : undefined
        msg = fromBody ?? e.message
      } else if (e instanceof Error) {
        msg = e.message
      } else {
        msg = String(e)
      }
      setError(msg)
    } finally { setScanning(false) }
  }

  // Exports the component's full finding list, not the severity slice on
  // screen: this is one artifact's record, and a partial one is a claim about
  // the artifact that isn't true.
  async function exportFindings(as: 'csv' | 'json') {
    setError('')
    try {
      const res = await nexspenceApi.exportScanResult(componentId.trim(), as)
      saveBlob(res.data as Blob, exportFilename(`scan-${componentId.trim()}`, as))
    } catch (e: unknown) {
      if (axios.isAxiosError(e)) setError(e.response?.data?.error ?? e.message)
    }
  }

  const findings = result?.findings ?? []
  const filtered = severityFilter === 'ALL' ? findings : findings.filter(f => f.severity === severityFilter)

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      <HoloCard>
        <div style={{ fontSize: 14, fontWeight: 600, color: 'var(--holo-text)', marginBottom: 12, display: 'flex', alignItems: 'center', gap: 8 }}>
          <Bug size={15} style={{ color: 'var(--holo-a)' }} /> Trivy Vulnerability Scan
        </div>
        <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' as const }}>
          <HoloInput style={{ flex: '1 1 180px' }} placeholder="Component ID (UUID)" value={componentId} onChange={e => setComponentId(e.target.value)} />
          <HoloInput style={{ flex: '2 1 260px' }} placeholder="Image ref override (optional, e.g. alpine:3.18)" value={imageRef} onChange={e => setImageRef(e.target.value)} />
          <HoloButton variant="primary" onClick={runScan} disabled={scanning || (scanner !== null && scanner.state !== 'ready')}>
            {scanning ? <Loader size={14} className="spin" /> : <Shield size={14} />}
            {scanning ? `Scanning… ${fmtElapsedSec(elapsed)}` : 'Scan'}
          </HoloButton>
        </div>
        {scanner && (
          <div style={{ marginTop: 8, fontSize: 12, lineHeight: 1.5, color: scanner.state === 'ready' ? 'var(--holo-text-dim)' : '#f59e0b' }}>
            {scanner.message}
            {(scanner.state === 'missing' || scanner.state === 'broken') && (
              <> — see <a href="https://github.com/nexspence/nexspence/blob/main/docs/scanning.md" target="_blank" rel="noreferrer" style={{ color: 'inherit' }}>how to enable image scanning</a></>
            )}
          </div>
        )}
        {scanning && (
          <div style={{ marginTop: 8, fontSize: 12, color: 'var(--holo-text-faint)', lineHeight: 1.5 }}>
            Running Trivy vulnerability scan{elapsed >= 20 ? ' — first run downloads the vulnerability DB, this may take 1–3 minutes' : ''}
            {elapsed >= 90 ? '; please wait…' : ''}
          </div>
        )}
        {error && <div role="alert" style={{ marginTop: 10, padding: '8px 12px', background: 'rgba(239,68,68,0.1)', border: '1px solid rgba(239,68,68,0.3)', borderRadius: 8, color: '#ef4444', fontSize: 13 }}>{error}</div>}
      </HoloCard>

      {result && result.status === 'failed' && result.error && (
        <div role="alert" style={{ padding: '12px 14px', background: 'rgba(239,68,68,0.1)', border: '1px solid rgba(239,68,68,0.35)', borderRadius: 10, color: '#fca5a5', fontSize: 13, lineHeight: 1.45 }}>
          {result.error}
        </div>
      )}

      {result && result.status === 'ok' && (
        <>
          {/* Summary cards */}
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(6, 1fr)', gap: 8 }}>
            {SEVERITY_ORDER.map(sev => (
              <HoloCard key={sev} style={{ padding: '12px 16px', textAlign: 'center' as const }}>
                <div style={{ fontSize: 20, fontWeight: 700, color: sevColor(sev) }}>{result.summary[sev.toLowerCase() as keyof ScanSummary] as number}</div>
                <div style={{ fontSize: 11, color: 'var(--holo-text-dim)', marginTop: 2 }}>{sev}</div>
              </HoloCard>
            ))}
          </div>

          {/* Status / meta */}
          <div style={{ display: 'flex', alignItems: 'center', gap: 10, fontSize: 12, color: 'var(--holo-text-dim)' }}>
            {result.status === 'ok' ? <CheckCircle size={14} style={{ color: '#22c55e' }} /> : <AlertTriangle size={14} style={{ color: '#f59e0b' }} />}
            <span>Image: <span style={{ color: 'var(--holo-text)' }}>{result.imageRef || '—'}</span></span>
            <span>·</span>
            <span>Scanned {new Date(result.scannedAt).toLocaleString()}</span>
            <span>·</span>
            <span>{result.summary.total} total CVEs</span>
            <span style={{ flex: 1 }} />
            <HoloButton onClick={() => exportFindings('csv')} title="Download these findings as CSV">
              <Download size={14} />
              Export CSV
            </HoloButton>
            <HoloButton onClick={() => exportFindings('json')} title="Download these findings as JSON">
              <Download size={14} />
              Export JSON
            </HoloButton>
          </div>

          {/* Severity filter */}
          <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap' as const }}>
            {(['ALL', ...SEVERITY_ORDER]).map(s => (
              <button key={s} onClick={() => setSeverityFilter(s)} style={{
                padding: '4px 12px', borderRadius: 6, border: 'none', cursor: 'pointer', fontSize: 12, fontWeight: 600,
                background: severityFilter === s ? (s === 'ALL' ? '#3b82f6' : sevColor(s)) : 'rgba(255,255,255,0.06)',
                color: severityFilter === s ? '#fff' : 'var(--holo-text-faint)',
              }}>{s} {s !== 'ALL' && `(${result.summary[s.toLowerCase() as keyof ScanSummary]})`}</button>
            ))}
          </div>

          {/* Findings table */}
          {filtered.length === 0 ? (
            <div style={emptyStyle}>{result.summary.total === 0 ? '✓ No vulnerabilities found' : 'No findings for selected severity'}</div>
          ) : (
            <HoloCard>
              <table className="holo-table" style={{ width: '100%', borderCollapse: 'collapse' as const, fontSize: 13 }}>
                <thead>
                  <tr style={{ color: 'var(--holo-text-faint)', textAlign: 'left' as const }}>
                    <th style={{ padding: '0 0 10px', fontWeight: 600 }}>CVE ID</th>
                    <th style={{ padding: '0 0 10px', fontWeight: 600 }}>Severity</th>
                    <th style={{ padding: '0 0 10px', fontWeight: 600 }}>Package</th>
                    <th style={{ padding: '0 0 10px', fontWeight: 600 }}>Installed</th>
                    <th style={{ padding: '0 0 10px', fontWeight: 600 }}>Fixed</th>
                    <th style={{ padding: '0 0 10px', fontWeight: 600 }}>Title</th>
                  </tr>
                </thead>
                <tbody>
                  {filtered.map((f, i) => (
                    <tr key={f.id + f.pkgName + i} style={{ borderTop: '1px solid rgba(255,255,255,0.05)' }}>
                      <td style={{ padding: '8px 0', ...monoStyle, color: 'var(--holo-a)' }}>{f.id}</td>
                      <td style={{ padding: '8px 8px 8px 0' }}><HoloPill style={{ background: sevColor(f.severity)+'22', color: sevColor(f.severity) }}>{f.severity}</HoloPill></td>
                      <td style={{ padding: '8px 8px 8px 0', color: 'var(--holo-text)' }}>{f.pkgName}</td>
                      <td style={{ padding: '8px 8px 8px 0', ...monoStyle, color: 'var(--holo-text-dim)' }}>{f.installedVersion}</td>
                      <td style={{ padding: '8px 8px 8px 0', ...monoStyle, color: '#22c55e' }}>{f.fixedVersion || '—'}</td>
                      <Truncated as="td" text={f.title || '—'} style={{ padding: '8px 0', color: 'var(--holo-text-dim)', maxWidth: 280 }} />
                    </tr>
                  ))}
                </tbody>
              </table>
            </HoloCard>
          )}
        </>
      )}

      {result && result.status === 'failed' && !result.error && (
        <div style={emptyStyle}>Scan failed — no details from scanner</div>
      )}
    </div>
  )
}

function VulnDashTab() {
  const [summary, setSummary] = useState<SecuritySummary | null>(null)
  const [items, setItems] = useState<VulnRow[]>([])
  const [total, setTotal] = useState(0)
  const [offset, setOffset] = useState(0)
  const [repoFilter, setRepoFilter] = useState('')
  const [severityFilter, setSeverityFilter] = useState('')
  const [scanning, setScanning] = useState(false)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  const LIMIT = 50
  // Responses are applied in resolution order, not request order: a broader,
  // earlier query resolving late must not overwrite the current one — the
  // table would show rows for a filter the input no longer contains (#337).
  const vulnReqSeq = useRef(0)

  async function loadSummary() {
    try {
      const res = await apiClient.get<SecuritySummary>('/api/v1/security/summary')
      setSummary(res.data)
    } catch { /* ignore */ }
  }

  async function loadVulns(reset = false, explicitOffset?: number) {
    const seq = ++vulnReqSeq.current
    const newOffset = reset ? 0 : (explicitOffset ?? offset)
    if (reset) setOffset(0)
    setLoading(true)
    try {
      const params: Record<string, string | number> = { limit: LIMIT, offset: newOffset }
      if (repoFilter) params.repo = repoFilter
      if (severityFilter) params.severity = severityFilter
      const res = await apiClient.get<{ items: VulnRow[]; total: number }>('/api/v1/security/vulnerabilities', { params })
      if (seq !== vulnReqSeq.current) return // superseded by a newer request
      setItems(reset ? res.data.items : [...items, ...res.data.items])
      setTotal(res.data.total)
    } catch (e: unknown) {
      if (seq !== vulnReqSeq.current) return
      if (axios.isAxiosError(e)) setError(e.response?.data?.error ?? e.message)
    } finally {
      if (seq === vulnReqSeq.current) setLoading(false)
    }
  }

  useEffect(() => {
    loadSummary()
    loadVulns(true)
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [repoFilter, severityFilter])

  // The export carries the filters that are on screen: a report that quietly
  // covers something other than what the user is looking at is worse than none.
  async function exportVulns(as: 'csv' | 'json') {
    setError('')
    try {
      const res = await nexspenceApi.exportVulnerabilities(
        { repo: repoFilter || undefined, severity: severityFilter || undefined },
        as,
      )
      saveBlob(res.data as Blob, exportFilename('vulnerabilities', as))
    } catch (e: unknown) {
      if (axios.isAxiosError(e)) setError(e.response?.data?.error ?? e.message)
    }
  }

  async function rescanAll() {
    setScanning(true)
    setError('')
    try {
      const body = repoFilter ? { repo: repoFilter } : {}
      await apiClient.post('/api/v1/security/scan/bulk', body)
      await loadSummary()
      await loadVulns(true)
    } catch (e: unknown) {
      if (axios.isAxiosError(e)) setError(e.response?.data?.error ?? e.message)
    } finally {
      setScanning(false)
    }
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      {/* Summary cards */}
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(7, 1fr)', gap: 8 }}>
        {(['malicious','critical','high','medium','low','unknown'] as const).map(sev => (
          <HoloCard key={sev} style={{ padding: '12px 16px', textAlign: 'center' as const }}>
            <div style={{ fontSize: 20, fontWeight: 700, color: sevColor(sev.toUpperCase()) }}>
              {summary ? summary[sev] : '—'}
            </div>
            <div style={{ fontSize: 11, color: 'var(--holo-text-dim)', marginTop: 2 }}>{sev.toUpperCase()}</div>
          </HoloCard>
        ))}
        <HoloCard style={{ padding: '12px 16px', textAlign: 'center' as const }}>
          <div style={{ fontSize: 20, fontWeight: 700, color: 'var(--holo-text)' }}>
            {summary ? summary.scanned_total : '—'}
          </div>
          <div style={{ fontSize: 11, color: 'var(--holo-text-dim)', marginTop: 2 }}>SCANNED</div>
        </HoloCard>
      </div>

      {/* Toolbar */}
      <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' as const, alignItems: 'center' }}>
        <HoloInput
          placeholder="Filter by repo"
          value={repoFilter}
          onChange={e => setRepoFilter(e.target.value)}
          style={{ flex: '1 1 160px' }}
        />
        <select
          value={severityFilter}
          onChange={e => setSeverityFilter(e.target.value)}
          style={{
            padding: '8px 12px', borderRadius: 8, border: '1px solid rgba(255,255,255,0.1)',
            background: 'rgba(255,255,255,0.05)', color: 'var(--holo-text)', fontSize: 13,
          }}
        >
          <option value="">All severities</option>
          {/* MALICIOUS leads and stands alone: the CVE tiers below it are
              minimums that also match malware, this one means malware only. */}
          {['MALICIOUS','CRITICAL','HIGH','MEDIUM','LOW'].map(s => (
            <option key={s} value={s}>{s}</option>
          ))}
        </select>
        <HoloButton onClick={() => exportVulns('csv')} title="Download the filtered results as CSV">
          <Download size={14} />
          Export CSV
        </HoloButton>
        <HoloButton onClick={() => exportVulns('json')} title="Download the filtered results as JSON">
          <Download size={14} />
          Export JSON
        </HoloButton>
        <HoloButton variant="primary" onClick={rescanAll} disabled={scanning}>
          {scanning ? <Loader size={14} className="spin" /> : <Shield size={14} />}
          {scanning ? 'Scanning…' : 'Rescan All'}
        </HoloButton>
      </div>

      {error && (
        <div role="alert" style={{ padding: '8px 12px', background: 'rgba(239,68,68,0.1)', border: '1px solid rgba(239,68,68,0.3)', borderRadius: 8, color: '#ef4444', fontSize: 13 }}>
          {error}
        </div>
      )}

      {/* Vulnerabilities table */}
      {loading && items.length === 0 ? (
        <div style={emptyStyle}>Loading…</div>
      ) : items.length === 0 ? (
        <div style={emptyStyle}>No vulnerabilities found — run a scan to populate this view</div>
      ) : (
        <HoloCard>
          <div style={{ fontSize: 12, color: 'var(--holo-text-dim)', marginBottom: 10 }}>
            Showing {items.length} of {total}
          </div>
          <table className="holo-table" style={{ width: '100%', borderCollapse: 'collapse' as const, fontSize: 13 }}>
            <thead>
              <tr style={{ color: 'var(--holo-text-faint)', textAlign: 'left' as const }}>
                <th style={{ padding: '0 8px 10px 0', fontWeight: 600 }}>Repo</th>
                <th style={{ padding: '0 8px 10px 0', fontWeight: 600 }}>Format</th>
                <th style={{ padding: '0 8px 10px 0', fontWeight: 600 }}>Component</th>
                <th style={{ padding: '0 8px 10px 0', fontWeight: 600 }}>Version</th>
                <th style={{ padding: '0 8px 10px 0', fontWeight: 600, color: sevColor('MALICIOUS') }}>MAL</th>
                <th style={{ padding: '0 8px 10px 0', fontWeight: 600, color: sevColor('CRITICAL') }}>C</th>
                <th style={{ padding: '0 8px 10px 0', fontWeight: 600, color: sevColor('HIGH') }}>H</th>
                <th style={{ padding: '0 8px 10px 0', fontWeight: 600, color: sevColor('MEDIUM') }}>M</th>
                <th style={{ padding: '0 8px 10px 0', fontWeight: 600, color: sevColor('LOW') }}>L</th>
                <th style={{ padding: '0 0 10px', fontWeight: 600 }}>Scanned</th>
              </tr>
            </thead>
            <tbody>
              {items.map((row) => (
                <tr key={row.componentId} style={{ borderTop: '1px solid rgba(255,255,255,0.05)' }}>
                  <td style={{ padding: '8px 8px 8px 0', color: 'var(--holo-a)' }}>{row.repoName}</td>
                  <td style={{ padding: '8px 8px 8px 0', color: 'var(--holo-text-dim)' }}>{row.format}</td>
                  <td style={{ padding: '8px 8px 8px 0', color: 'var(--holo-text)' }}>{row.name}</td>
                  <td style={{ padding: '8px 8px 8px 0', ...monoStyle, color: 'var(--holo-text-dim)' }}>{row.version}</td>
                  <td style={{ padding: '8px 8px 8px 0', fontWeight: 700, color: row.malicious > 0 ? sevColor('MALICIOUS') : 'var(--holo-text-faint)' }}>{row.malicious}</td>
                  <td style={{ padding: '8px 8px 8px 0', fontWeight: 700, color: row.critical > 0 ? sevColor('CRITICAL') : 'var(--holo-text-faint)' }}>{row.critical}</td>
                  <td style={{ padding: '8px 8px 8px 0', fontWeight: row.high > 0 ? 700 : 400, color: row.high > 0 ? sevColor('HIGH') : 'var(--holo-text-faint)' }}>{row.high}</td>
                  <td style={{ padding: '8px 8px 8px 0', color: row.medium > 0 ? sevColor('MEDIUM') : 'var(--holo-text-faint)' }}>{row.medium}</td>
                  <td style={{ padding: '8px 8px 8px 0', color: row.low > 0 ? sevColor('LOW') : 'var(--holo-text-faint)' }}>{row.low}</td>
                  <td style={{ padding: '8px 0', color: 'var(--holo-text-dim)', fontSize: 11 }}>
                    {new Date(row.scannedAt).toLocaleDateString()}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          {items.length < total && (
            <div style={{ marginTop: 12, textAlign: 'center' as const }}>
              <HoloButton disabled={loading} onClick={() => { const next = items.length; setOffset(next); loadVulns(false, next) }}>
                {loading ? 'Loading…' : 'Load more'}
              </HoloButton>
            </div>
          )}
        </HoloCard>
      )}
    </div>
  )
}

const WEBHOOK_EVENTS = ['artifact.published', 'artifact.deleted', 'repo.created', 'proxy.error']

const WEBHOOK_TEMPLATES = [
  { label: 'Slack',         events: ['artifact.published'],                                                         urlHint: 'https://hooks.slack.com/services/…' },
  { label: 'CI/CD Trigger', events: ['artifact.published', 'artifact.deleted'],                                     urlHint: '' },
  { label: 'Audit Logger',  events: ['artifact.published', 'artifact.deleted', 'repo.created', 'proxy.error'],      urlHint: '' },
  { label: 'Proxy Monitor', events: ['proxy.error'],                                                                urlHint: '' },
] as const

function WebhooksTab() {
  const qc = useQueryClient()
  const { data: hooks = [], isLoading, refetch } = useQuery<WebhookDef[]>({
    queryKey: ['webhooks'],
    queryFn: () => apiClient.get<WebhookDef[]>('/api/v1/webhooks').then(r => r.data),
  })
  const [showForm, setShowForm] = useState(false)
  const [form, setForm] = useState({ name: '', url: '', events: WEBHOOK_EVENTS, secret: '', active: true })
  const [saving, setSaving] = useState(false)

  const [testResults, setTestResults] = useState<Record<string, { ok: boolean; msg: string } | null>>({})

  async function testHook(id: string) {
    setTestResults(r => ({ ...r, [id]: null })) // null = loading
    try {
      const res = await apiClient.post<{ status: number; latency_ms: number }>(`/api/v1/webhooks/${id}/test`)
      const ok = res.data.status >= 200 && res.data.status < 300
      setTestResults(r => ({ ...r, [id]: { ok, msg: `${ok ? '✓' : '✗'} ${res.data.status} (${res.data.latency_ms}ms)` } }))
    } catch (e) {
      const msg = apiErrorMessage(e, 'error')
      setTestResults(r => ({ ...r, [id]: { ok: false, msg: `✗ ${msg}` } }))
    }
    setTimeout(() => setTestResults(r => { const next = { ...r }; delete next[id]; return next }), 5000)
  }

  const [editingId, setEditingId] = useState<string | null>(null)
  const [editForm, setEditForm] = useState({ name: '', url: '', events: WEBHOOK_EVENTS, secret: '', active: true })

  function startEdit(h: WebhookDef) {
    setEditingId(h.id)
    setEditForm({ name: h.name, url: h.url, events: h.events, secret: h.secret ?? '', active: h.active })
  }

  async function saveEdit() {
    if (!editingId) return
    setSaving(true)
    try {
      await apiClient.put(`/api/v1/webhooks/${editingId}`, editForm)
      qc.invalidateQueries({ queryKey: ['webhooks'] })
      setEditingId(null)
    } finally { setSaving(false) }
  }

  const toggleEditEvent = (ev: string) => setEditForm(f => ({
    ...f, events: f.events.includes(ev) ? f.events.filter(e => e !== ev) : [...f.events, ev]
  }))

  async function save() {
    setSaving(true)
    try {
      await apiClient.post('/api/v1/webhooks', form)
      qc.invalidateQueries({ queryKey: ['webhooks'] })
      setShowForm(false)
      setForm({ name: '', url: '', events: WEBHOOK_EVENTS, secret: '', active: true })
    } finally { setSaving(false) }
  }

  const del = useMutation({
    mutationFn: (id: string) => apiClient.delete(`/api/v1/webhooks/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['webhooks'] }),
  })

  const toggleEvent = (ev: string) => setForm(f => ({
    ...f, events: f.events.includes(ev) ? f.events.filter(e => e !== ev) : [...f.events, ev]
  }))

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <div style={{ fontSize: 14, color: 'var(--holo-text-dim)' }}>HTTP callbacks fired on repository events</div>
        <div style={{ display: 'flex', gap: 8 }}>
          <HoloButton onClick={() => refetch()}><RefreshCw size={14} /></HoloButton>
          <HoloButton variant="primary" icon={<Plus size={14} />} onClick={() => setShowForm(v => !v)}>New Webhook</HoloButton>
        </div>
      </div>

      {showForm && (
        <HoloCard>
          <div style={{ fontSize: 14, fontWeight: 600, color: 'var(--holo-text)', marginBottom: 12 }}>New Webhook</div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
            <div style={{ marginBottom: 4 }}>
              <div style={{ fontSize: 12, color: 'var(--holo-text-dim)', marginBottom: 6 }}>Quick start:</div>
              <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap' as const }}>
                {WEBHOOK_TEMPLATES.map(t => (
                  <HoloButton key={t.label} style={{ fontSize: 11, padding: '3px 10px' }}
                    onClick={() => setForm(f => ({
                      ...f,
                      events: [...t.events],
                      url: t.urlHint || f.url,
                    }))}>
                    {t.label}
                  </HoloButton>
                ))}
              </div>
            </div>
            <HoloInput placeholder="Name" value={form.name} onChange={e => setForm(f => ({ ...f, name: e.target.value }))} />
            <HoloInput placeholder="URL (https://...)" value={form.url} onChange={e => setForm(f => ({ ...f, url: e.target.value }))} />
            <HoloInput placeholder="HMAC secret (optional)" value={form.secret} onChange={e => setForm(f => ({ ...f, secret: e.target.value }))} />
            <div style={{ fontSize: 12, color: 'var(--holo-text-dim)', marginBottom: 4 }}>Events to subscribe:</div>
            <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' as const }}>
              {WEBHOOK_EVENTS.map(ev => (
                <label key={ev} style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 13, color: form.events.includes(ev) ? 'var(--holo-text)' : 'var(--holo-text-faint)', cursor: 'pointer' }}>
                  <input type="checkbox" checked={form.events.includes(ev)} onChange={() => toggleEvent(ev)} style={{ accentColor: 'var(--holo-a)' }} />
                  <span style={{ fontFamily: 'monospace', fontSize: 12 }}>{ev}</span>
                </label>
              ))}
            </div>
            <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
              <HoloButton onClick={() => setShowForm(false)}>Cancel</HoloButton>
              <HoloButton variant="primary" onClick={save} disabled={saving || !form.name || !form.url}>{saving ? 'Saving…' : 'Create'}</HoloButton>
            </div>
          </div>
        </HoloCard>
      )}

      {isLoading ? <div style={emptyStyle}>Loading…</div> : hooks.length === 0 ? <HoloCard style={emptyStyle}>No webhooks configured</HoloCard> : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          {hooks.map(h => (
            <HoloCard key={h.id} style={{ padding: 0 }}>
              {editingId === h.id ? (
                <div style={{ display: 'flex', flexDirection: 'column', gap: 10, padding: '16px 18px' }}>
                  <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--holo-text)' }}>Edit Webhook</div>
                  <HoloInput placeholder="Name" value={editForm.name} onChange={e => setEditForm(f => ({ ...f, name: e.target.value }))} />
                  <HoloInput placeholder="URL" value={editForm.url} onChange={e => setEditForm(f => ({ ...f, url: e.target.value }))} />
                  <HoloInput placeholder="HMAC secret (leave blank to keep)" value={editForm.secret} onChange={e => setEditForm(f => ({ ...f, secret: e.target.value }))} />
                  <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13, color: 'var(--holo-text)', cursor: 'pointer' }}>
                    <input type="checkbox" checked={editForm.active} onChange={e => setEditForm(f => ({ ...f, active: e.target.checked }))} style={{ accentColor: 'var(--holo-a)' }} />
                    Active
                  </label>
                  <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' as const }}>
                    {WEBHOOK_EVENTS.map(ev => (
                      <label key={ev} style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 13, color: editForm.events.includes(ev) ? 'var(--holo-text)' : 'var(--holo-text-faint)', cursor: 'pointer' }}>
                        <input type="checkbox" checked={editForm.events.includes(ev)} onChange={() => toggleEditEvent(ev)} style={{ accentColor: 'var(--holo-a)' }} />
                        <span style={{ fontFamily: 'monospace', fontSize: 12 }}>{ev}</span>
                      </label>
                    ))}
                  </div>
                  <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
                    <HoloButton onClick={() => setEditingId(null)}>Cancel</HoloButton>
                    <HoloButton variant="primary" onClick={saveEdit} disabled={saving || !editForm.name || !editForm.url}>{saving ? 'Saving…' : 'Save'}</HoloButton>
                  </div>
                </div>
              ) : (
                <div style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '10px 18px' }}>
                  <Webhook size={14} style={{ color: h.active ? '#22c55e' : '#6b7280', flexShrink: 0 }} />
                  <div style={{ flex: 1 }}>
                    <div style={{ color: 'var(--holo-text)', fontWeight: 600 }}>{h.name}</div>
                    <div style={{ color: 'var(--holo-text-faint)', ...monoStyle }}>{h.url}</div>
                    <div style={{ display: 'flex', gap: 4, marginTop: 4, flexWrap: 'wrap' as const }}>
                      {h.events.map(ev => <span key={ev} style={{ fontSize: 10, padding: '1px 6px', borderRadius: 4, background: 'rgba(124,92,255,0.12)', color: 'var(--holo-a)', fontFamily: 'monospace' }}>{ev}</span>)}
                    </div>
                  </div>
                  <HoloPill tone={h.active ? 'success' : 'default'}>{h.active ? 'active' : 'inactive'}</HoloPill>
                  {testResults[h.id] !== undefined && (
                    <span style={{ fontSize: 12, fontFamily: 'monospace', color: testResults[h.id]?.ok ? '#22c55e' : '#ef4444' }}>
                      {testResults[h.id] === null ? '…' : testResults[h.id]?.msg}
                    </span>
                  )}
                  <HoloButton onClick={() => testHook(h.id)} title="Send test event"><Zap size={13} /></HoloButton>
                  <HoloButton onClick={() => startEdit(h)} title="Edit"><Pencil size={13} /></HoloButton>
                  <HoloButton variant="danger" onClick={() => del.mutate(h.id)}><Trash2 size={13} /></HoloButton>
                </div>
              )}
            </HoloCard>
          ))}
        </div>
      )}
    </div>
  )
}

interface ContentSelector { id: string; name: string; description: string; expression: string }

function PrivilegesTab({ admin }: { admin: boolean }) {
  const qc = useQueryClient()
  const { data: privs = [], isLoading } = useQuery<Privilege[]>({
    queryKey: ['privileges'],
    queryFn: () => nexusApi.listPrivileges().then(r => r.data),
  })
  const { data: selectors = [] } = useQuery<ContentSelector[]>({
    queryKey: ['content-selectors'],
    queryFn: () => nexusApi.listContentSelectors().then(r => r.data),
  })
  const { data: privRoleMap = {} } = useQuery<Record<string, string[]>>({
    queryKey: ['privilege-role-map'],
    queryFn: () => nexspenceApi.privilegeRoleMap(),
  })
  const PRIV_ACTIONS = ['read', 'browse', 'write', 'delete'] as const

  const [showModal, setShowModal] = useState(false)
  const [editing, setEditing] = useState<Privilege | null>(null)
  const [form, setForm] = useState({ name: '', description: '', contentSelectorId: '', actions: [] as string[] })
  const [saveError, setSaveError] = useState('')
  const [privSearch, setPrivSearch] = useState('')

  const filteredPrivs = privs.filter(p => {
    if (!privSearch.trim()) return true
    const q = privSearch.toLowerCase()
    return p.name.toLowerCase().includes(q) || (p.description ?? '').toLowerCase().includes(q)
  })

  function openCreate() {
    setEditing(null)
    setForm({ name: '', description: '', contentSelectorId: '', actions: [] })
    setSaveError('')
    setShowModal(true)
  }

  function openEdit(p: Privilege) {
    setEditing(p)
    setForm({
      name: p.name,
      description: p.description,
      contentSelectorId: p.contentSelectorId ?? '',
      actions: (p.attrs?.actions as string[] | undefined) ?? [],
    })
    setSaveError('')
    setShowModal(true)
  }

  const save = useMutation({
    mutationFn: async () => {
      if (!form.contentSelectorId) throw new Error('Select a content selector')
      const payload = {
        name: form.name,
        description: form.description,
        type: 'repository-content-selector',
        contentSelectorId: form.contentSelectorId,
        attrs: { actions: form.actions },
      }
      if (editing) return nexusApi.updatePrivilege(editing.id, payload)
      return nexusApi.createPrivilege(payload)
    },
    onSuccess: () => { qc.invalidateQueries({ queryKey: ['privileges'] }); setShowModal(false) },
    onError: (e: unknown) => {
      let msg = 'Error'
      if (axios.isAxiosError(e)) {
        const d = e.response?.data
        if (typeof d === 'object' && d !== null && 'error' in d) msg = String((d as { error: unknown }).error)
      } else if (e instanceof Error) { msg = e.message }
      setSaveError(msg)
    },
  })

  const del = useMutation({
    mutationFn: (id: string) => nexusApi.deletePrivilege(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['privileges'] }),
  })

  const selectedSelector = selectors.find(s => s.id === form.contentSelectorId)

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      {admin && (
        <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
          <HoloButton variant="primary" icon={<Plus size={14} />} onClick={openCreate}>New Privilege</HoloButton>
        </div>
      )}

      <HoloInput
        placeholder="Search privileges…"
        value={privSearch}
        onChange={e => setPrivSearch(e.target.value)}
      />

      {isLoading ? <div style={emptyStyle}>Loading…</div> : privs.length === 0 ? <div style={emptyStyle}>No privileges</div> : filteredPrivs.length === 0 ? <div style={emptyStyle}>No matches</div> : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
          {filteredPrivs.map(p => {
            const actions = (p.attrs?.actions as string[] | undefined) ?? []
            const typeColor = PRIV_TYPE_COLOR[p.type] ?? '#6b7280'
            const usedInRoles = privRoleMap[p.id] ?? []
            return (
              <div key={p.id} style={{
                display: 'grid', gridTemplateColumns: 'auto 1fr auto',
                alignItems: 'center', gap: 12, padding: '11px 16px',
                background: 'rgba(10,8,28,0.97)', border: '1px solid rgba(124,92,255,0.2)',
                borderRadius: 10, transition: 'border-color 0.15s, background 0.15s',
              }}
              onMouseEnter={e => { (e.currentTarget as HTMLDivElement).style.borderColor = 'rgba(124,92,255,0.45)'; (e.currentTarget as HTMLDivElement).style.background = 'rgba(124,92,255,0.04)' }}
              onMouseLeave={e => { (e.currentTarget as HTMLDivElement).style.borderColor = 'rgba(124,92,255,0.2)'; (e.currentTarget as HTMLDivElement).style.background = 'rgba(10,8,28,0.97)' }}
              >
                <span style={{ fontSize: 10, fontWeight: 600, padding: '2px 8px', borderRadius: 4, textTransform: 'uppercase' as const, letterSpacing: '0.3px', whiteSpace: 'nowrap' as const, background: typeColor + '22', color: typeColor }}>
                  {(p.type as string) === 'repository-content-selector' ? 'cs' : p.type.replace('repository-', '')}
                </span>
                <div style={{ minWidth: 0 }}>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 6, flexWrap: 'wrap' as const }}>
                    <span style={{ fontSize: 13, fontWeight: 600, color: 'var(--holo-text)' }}>{p.name}</span>
                    {p.readOnly && <HoloPill style={{ fontSize: 11 }}>built-in</HoloPill>}
                  </div>
                  {p.description && <div style={{ fontSize: 11, color: 'var(--holo-text-faint)', marginTop: 1 }}>{p.description}</div>}
                  <div style={{ display: 'flex', gap: 4, flexWrap: 'wrap' as const, marginTop: 4, alignItems: 'center' }}>
                    {actions.map(a => {
                      const ac = (a === 'write' || a === 'delete') ? '#f59e0b' : '#22c55e'
                      return <HoloPill key={a} style={{ background: ac + '22', color: ac, fontSize: 10 }}>{a}</HoloPill>
                    })}
                    {usedInRoles.map(roleName => (
                      <span key={roleName} style={{ fontSize: 10, padding: '1px 6px', borderRadius: 4, background: 'rgba(6,182,212,0.12)', color: '#67e8f9' }}>{roleName}</span>
                    ))}
                  </div>
                </div>
                <div style={{ display: 'flex', gap: 6 }}>
                  {admin && !p.readOnly && (
                    <>
                      <HoloButton style={{ padding: '4px 8px' }} onClick={() => openEdit(p)}>Edit</HoloButton>
                      <HoloButton variant="danger" style={{ padding: '4px 8px' }} onClick={() => { if (confirm(`Delete ${p.name}?`)) del.mutate(p.id) }}><Trash2 size={13} /></HoloButton>
                    </>
                  )}
                </div>
              </div>
            )
          })}
        </div>
      )}

      <HoloModal open={showModal} onClose={() => setShowModal(false)}>
        <h3 style={{ margin: 0, fontSize: 16, fontWeight: 700, color: 'var(--holo-text)' }}>{editing ? 'Edit Privilege' : 'New Privilege'}</h3>

        <HoloInput placeholder="Name *" value={form.name}
          onChange={e => setForm(f => ({ ...f, name: e.target.value }))} />
        <HoloInput placeholder="Description (optional)" value={form.description}
          onChange={e => setForm(f => ({ ...f, description: e.target.value }))} />

        <div>
          <div style={{ fontSize: 12, color: 'var(--holo-text-dim)', marginBottom: 6, display: 'flex', alignItems: 'center', gap: 8 }}>
            Actions
            <button
              onClick={() => {
                const allSelected = PRIV_ACTIONS.every(a => form.actions.includes(a))
                setForm(f => ({ ...f, actions: allSelected ? [] : [...PRIV_ACTIONS] }))
              }}
              style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'var(--holo-a)', fontSize: 12, padding: '0 4px' }}
            >
              {PRIV_ACTIONS.every(a => form.actions.includes(a)) ? 'Deselect all' : 'Select all'}
            </button>
          </div>
          <div style={{ display: 'flex', gap: 16, flexWrap: 'wrap' as const }}>
            {PRIV_ACTIONS.map(a => (
              <label key={a} style={{ display: 'flex', alignItems: 'center', gap: 6, cursor: 'pointer', fontSize: 13, color: form.actions.includes(a) ? 'var(--holo-text)' : 'var(--holo-text-dim)' }}>
                <input
                  type="checkbox"
                  checked={form.actions.includes(a)}
                  onChange={e => setForm(f => ({ ...f, actions: e.target.checked ? [...f.actions, a] : f.actions.filter(x => x !== a) }))}
                  style={{ accentColor: 'var(--holo-a)' }}
                />
                {a}
              </label>
            ))}
          </div>
        </div>

        <div>
          <div style={{ fontSize: 12, color: 'var(--holo-text-dim)', marginBottom: 4 }}>Content Selector *</div>
          <Select
            searchable
            options={[
              { value: '', label: '— select a content selector —' },
              ...selectors.map(s => ({ value: s.id, label: s.name })),
            ]}
            value={form.contentSelectorId}
            onChange={v => setForm(f => ({ ...f, contentSelectorId: v }))}
          />
          {selectedSelector && (
            <div style={{ marginTop: 6, padding: '6px 10px', background: 'rgba(6,182,212,0.08)', borderRadius: 8, fontSize: 12, color: '#67e8f9', fontFamily: 'monospace' }}>
              {selectedSelector.expression}
            </div>
          )}
          {selectors.length === 0 && (
            <div style={{ marginTop: 6, fontSize: 12, color: 'rgba(239,68,68,0.7)' }}>
              No content selectors defined — create one in the Content Selectors tab first.
            </div>
          )}
        </div>

        {saveError && (
          <div style={{ padding: '8px 12px', background: 'rgba(239,68,68,0.1)', border: '1px solid rgba(239,68,68,0.3)', borderRadius: 8, color: '#ef4444', fontSize: 12 }}>{saveError}</div>
        )}

        <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end', marginTop: 4 }}>
          <HoloButton onClick={() => setShowModal(false)}>Cancel</HoloButton>
          <HoloButton variant="primary" onClick={() => save.mutate()} disabled={save.isPending || !form.name.trim() || !form.contentSelectorId}>
            {save.isPending ? 'Saving…' : 'Save'}
          </HoloButton>
        </div>
      </HoloModal>
    </div>
  )
}

function ContentSelectorsTab({ admin }: { admin: boolean }) {
  const qc = useQueryClient()
  const { data: selectors = [], isLoading } = useQuery<{ id: string; name: string; description: string; expression: string }[]>({
    queryKey: ['content-selectors'],
    queryFn: () => nexusApi.listContentSelectors().then(r => r.data),
  })
  const { data: allRepos = [] } = useQuery<{ name: string; format: string; type: string }[]>({
    queryKey: ['repositories'],
    queryFn: () => nexusApi.listRepositories().then(r => r.data),
  })
  const { data: privs = [] } = useQuery<Privilege[]>({
    queryKey: ['privileges'],
    queryFn: () => nexusApi.listPrivileges().then(r => r.data),
  })
  const selectorToPriv = useMemo(
    () => new Map(privs.filter(p => !!p.contentSelectorId).map(p => [p.contentSelectorId!, p.name])),
    [privs]
  )

  const [showModal, setShowModal] = useState(false)
  const [editing, setEditing] = useState<{ id: string; name: string; description: string; expression: string } | null>(null)
  const [form, setForm] = useState({ name: '', description: '', repo: '', path: '' })
  const [repoSearch, setRepoSearch] = useState('')
  const [pathSearch, setPathSearch] = useState('')
  const [saveError, setSaveError] = useState('')
  const [repoOpen, setRepoOpen] = useState(false)
  const [pathOpen, setPathOpen] = useState(false)
  const [legacyExpr, setLegacyExpr] = useState('')
  const [csSearch, setCsSearch] = useState('')

  const filteredSelectors = selectors.filter(s => {
    if (!csSearch.trim()) return true
    const q = csSearch.toLowerCase()
    return s.name.toLowerCase().includes(q)
      || (s.description ?? '').toLowerCase().includes(q)
      || s.expression.toLowerCase().includes(q)
  })

  const { data: pathTree, isLoading: pathsLoading } = useQuery<{ paths: string[] }>({
    queryKey: ['path-tree', form.repo, pathSearch],
    queryFn: () => nexusApi.listPathTree(form.repo, pathSearch || undefined).then(r => r.data),
    enabled: !!form.repo,
  })

  function buildExpression(repo: string, path: string): string {
    if (repo && path) return `repository == "${repo}" && path.startsWith("${path}")`
    if (repo)         return `repository == "${repo}"`
    if (path)         return `path.startsWith("${path}")`
    return ''
  }

  function parseExpression(expr: string): { repo: string; path: string } {
    const full = expr.match(/^repository == "([^"]+)" && path\.startsWith\("([^"]+)"\)$/)
    if (full) return { repo: full[1], path: full[2] }
    const repoOnly = expr.match(/^repository == "([^"]+)"$/)
    if (repoOnly) return { repo: repoOnly[1], path: '' }
    const pathOnly = expr.match(/^path\.startsWith\("([^"]+)"\)$/)
    if (pathOnly) return { repo: '', path: pathOnly[1] }
    return { repo: '', path: '' }
  }

  function selectorSummary(expr: string): string {
    const { repo, path } = parseExpression(expr)
    const displayPath = path.replace(/^\//, '').replace(/\/$/, '') // "/da/bas/" → "da/bas"
    if (repo && path) return `${displayPath}/* in ${repo}`
    if (repo)         return `all paths in ${repo}`
    if (path)         return `${displayPath}/* in all repos`
    return expr
  }

  function openCreate() {
    setEditing(null)
    setForm({ name: '', description: '', repo: '', path: '' })
    setRepoSearch(''); setPathSearch(''); setSaveError('')
    setRepoOpen(false); setPathOpen(false); setLegacyExpr('')
    setShowModal(true)
  }

  function openEdit(s: { id: string; name: string; description: string; expression: string }) {
    setEditing(s)
    const { repo, path } = parseExpression(s.expression)
    const isLegacy = !repo && !path && !!s.expression
    setLegacyExpr(isLegacy ? s.expression : '')
    setForm({ name: s.name, description: s.description, repo, path })
    setRepoSearch(repo); setPathSearch(path); setSaveError('')
    setRepoOpen(false); setPathOpen(false)
    setShowModal(true)
  }

  const save = useMutation({
    mutationFn: async () => {
      const expression = legacyExpr || buildExpression(form.repo, form.path)
      if (!expression) throw new Error('Select a repository or path')
      const payload = { name: form.name, description: form.description, expression }
      if (editing) return nexusApi.updateContentSelector(editing.id, payload)
      return nexusApi.createContentSelector(payload)
    },
    onSuccess: () => { qc.invalidateQueries({ queryKey: ['content-selectors'] }); setShowModal(false) },
    onError: (e: unknown) => {
      let msg = 'Error'
      if (axios.isAxiosError(e)) {
        const d = e.response?.data
        if (typeof d === 'object' && d !== null && 'error' in d) msg = String((d as { error: unknown }).error)
      } else if (e instanceof Error) { msg = e.message }
      setSaveError(msg)
    },
  })

  const del = useMutation({
    mutationFn: (id: string) => nexusApi.deleteContentSelector(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['content-selectors'] }),
  })

  const filteredRepos = allRepos.filter(r =>
    !repoSearch || r.name.toLowerCase().includes(repoSearch.toLowerCase())
  )

  const paths = pathTree?.paths ?? []
  const canSave = !!form.name.trim() && (!!form.repo || !!form.path || !!legacyExpr)

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      {admin && (
        <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
          <HoloButton variant="primary" icon={<Plus size={14} />} onClick={openCreate}>New Selector</HoloButton>
        </div>
      )}

      <HoloInput
        placeholder="Search content selectors…"
        value={csSearch}
        onChange={e => setCsSearch(e.target.value)}
      />

      {isLoading ? <div style={emptyStyle}>Loading…</div> : selectors.length === 0 ? <div style={emptyStyle}>No content selectors</div> : filteredSelectors.length === 0 ? <div style={emptyStyle}>No matches</div> : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
          {filteredSelectors.map(s => {
            const linkedPriv = selectorToPriv.get(s.id)
            return (
              <div key={s.id} style={{
                display: 'grid', gridTemplateColumns: '1fr auto auto',
                alignItems: 'center', gap: 12, padding: '11px 16px',
                background: 'rgba(10,8,28,0.97)', border: '1px solid rgba(124,92,255,0.2)',
                borderRadius: 10, transition: 'border-color 0.15s, background 0.15s',
              }}
              onMouseEnter={e => { (e.currentTarget as HTMLDivElement).style.borderColor = 'rgba(124,92,255,0.45)'; (e.currentTarget as HTMLDivElement).style.background = 'rgba(124,92,255,0.04)' }}
              onMouseLeave={e => { (e.currentTarget as HTMLDivElement).style.borderColor = 'rgba(124,92,255,0.2)'; (e.currentTarget as HTMLDivElement).style.background = 'rgba(10,8,28,0.97)' }}
              >
                <div style={{ minWidth: 0 }}>
                  <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--holo-text)' }}>{s.name}</div>
                  <code style={{ fontFamily: 'monospace', fontSize: 11, color: 'var(--holo-a)', marginTop: 2, display: 'block' }}>{selectorSummary(s.expression)}</code>
                  {s.description && <div style={{ fontSize: 11, color: 'var(--holo-text-faint)', marginTop: 1 }}>{s.description}</div>}
                </div>
                <div>
                  {linkedPriv
                    ? <span style={{ fontSize: 11, padding: '2px 8px', borderRadius: 4, background: 'rgba(6,182,212,0.12)', color: '#67e8f9', whiteSpace: 'nowrap' as const }}>{linkedPriv}</span>
                    : <span style={{ color: 'var(--holo-text-faint)', fontSize: 12 }}>—</span>
                  }
                </div>
                <div style={{ display: 'flex', gap: 6 }}>
                  {admin && (
                    <>
                      <HoloButton style={{ padding: '4px 8px' }} onClick={() => openEdit(s)}>Edit</HoloButton>
                      <HoloButton variant="danger" style={{ padding: '4px 8px' }} onClick={() => { if (confirm(`Delete ${s.name}?`)) del.mutate(s.id) }}><Trash2 size={13} /></HoloButton>
                    </>
                  )}
                </div>
              </div>
            )
          })}
        </div>
      )}

      <HoloModal open={showModal} onClose={() => setShowModal(false)}>
        <h3 style={{ margin: 0, fontSize: 16, fontWeight: 700, color: 'var(--holo-text)' }}>
          {editing ? 'Edit Content Selector' : 'New Content Selector'}
        </h3>

        <HoloInput placeholder="Name *" value={form.name}
          onChange={e => setForm(f => ({ ...f, name: e.target.value }))} />
        <HoloInput placeholder="Description (optional)" value={form.description}
          onChange={e => setForm(f => ({ ...f, description: e.target.value }))} />

        {/* Repository dropdown */}
        <div>
          <div style={{ fontSize: 12, color: 'var(--holo-text-dim)', marginBottom: 4 }}>Repository</div>
          <HoloInput
            placeholder="Search repositories…"
            value={repoSearch}
            onFocus={() => setRepoOpen(true)}
            onChange={e => { setRepoSearch(e.target.value); setForm(f => ({ ...f, repo: '', path: '' })); setRepoOpen(true) }}
          />
          {repoOpen && (
            <div style={{ maxHeight: 160, overflowY: 'auto', background: 'rgba(8,6,18,0.95)', border: '1px solid rgba(255,255,255,0.1)', borderRadius: 8, marginTop: 4 }}>
              <div
                style={{ padding: '7px 12px', fontSize: 13, cursor: 'pointer', color: 'var(--holo-text-dim)',
                  background: !form.repo ? 'rgba(124,92,255,0.15)' : 'transparent' }}
                onClick={() => { setForm(f => ({ ...f, repo: '', path: '' })); setRepoSearch(''); setRepoOpen(false) }}
              >
                Any repository
              </div>
              {filteredRepos.map(r => (
                <div
                  key={r.name}
                  style={{ padding: '7px 12px', fontSize: 13, cursor: 'pointer',
                    color: form.repo === r.name ? 'var(--holo-a)' : 'var(--holo-text)',
                    background: form.repo === r.name ? 'rgba(124,92,255,0.15)' : 'transparent' }}
                  onClick={() => { setForm(f => ({ ...f, repo: r.name, path: '' })); setRepoSearch(r.name); setPathSearch(''); setRepoOpen(false) }}
                >
                  {r.name}
                  <span style={{ fontSize: 11, color: 'var(--holo-text-faint)', marginLeft: 8 }}>{r.format}</span>
                </div>
              ))}
              {filteredRepos.length === 0 && (
                <div style={{ padding: '7px 12px', fontSize: 13, color: 'var(--holo-text-faint)' }}>No repositories found</div>
              )}
            </div>
          )}
        </div>

        {/* Path dropdown */}
        <div>
          <div style={{ fontSize: 12, color: 'var(--holo-text-dim)', marginBottom: 4 }}>
            Path prefix {!form.repo && <span style={{ color: 'var(--holo-text-faint)' }}>(select a repository first)</span>}
          </div>
          <HoloInput
            style={{ opacity: !form.repo ? 0.4 : 1 }}
            placeholder="Search paths…"
            disabled={!form.repo}
            value={pathSearch}
            onFocus={() => setPathOpen(true)}
            onChange={e => { setPathSearch(e.target.value); setForm(f => ({ ...f, path: '' })); setPathOpen(true) }}
          />
          {form.repo && pathOpen && (
            <div style={{ maxHeight: 180, overflowY: 'auto', background: 'rgba(8,6,18,0.95)', border: '1px solid rgba(255,255,255,0.1)', borderRadius: 8, marginTop: 4 }}>
              <div
                style={{ padding: '7px 12px', fontSize: 13, cursor: 'pointer', color: 'var(--holo-text-dim)',
                  background: !form.path ? 'rgba(124,92,255,0.15)' : 'transparent' }}
                onClick={() => { setForm(f => ({ ...f, path: '' })); setPathSearch(''); setPathOpen(false) }}
              >
                Any path
              </div>
              {pathsLoading ? (
                <div style={{ padding: '7px 12px', fontSize: 13, color: 'var(--holo-text-faint)' }}>Loading…</div>
              ) : paths.length === 0 ? (
                <div style={{ padding: '7px 12px', fontSize: 13, color: 'var(--holo-text-faint)' }}>No paths found</div>
              ) : paths.map(p => {
                // strip leading/trailing slashes for display: "/da/bas/" → "da/bas"
                const label = p.replace(/^\//, '').replace(/\/$/, '')
                const depth = (label.match(/\//g) ?? []).length
                return (
                  <div
                    key={p}
                    style={{ padding: '6px 12px', paddingLeft: 12 + depth * 14, fontSize: 13, cursor: 'pointer',
                      color: form.path === p ? 'var(--holo-a)' : 'var(--holo-text)',
                      background: form.path === p ? 'rgba(124,92,255,0.15)' : 'transparent',
                      fontFamily: 'monospace' }}
                    onClick={() => { setForm(f => ({ ...f, path: p })); setPathSearch(label); setPathOpen(false) }}
                  >
                    {label}
                  </div>
                )
              })}
            </div>
          )}
        </div>

        {/* CEL preview */}
        {(form.repo || form.path) && (
          <div style={{ padding: '6px 10px', background: 'rgba(59,130,246,0.08)', borderRadius: 8, fontSize: 12, color: '#93c5fd', fontFamily: 'monospace' }}>
            {buildExpression(form.repo, form.path)}
          </div>
        )}

        {legacyExpr !== undefined && editing && !form.repo && !form.path && editing.expression && (
          <div>
            <div style={{ fontSize: 12, color: 'var(--holo-text-dim)', marginBottom: 4 }}>
              CEL Expression (legacy — not parseable as simple selector)
            </div>
            <textarea
              className="holo-input"
              style={{ fontFamily: 'monospace', fontSize: 12, height: 64, resize: 'vertical' as const }}
              value={legacyExpr}
              onChange={e => setLegacyExpr(e.target.value)}
            />
          </div>
        )}

        {saveError && (
          <div style={{ padding: '8px 12px', background: 'rgba(239,68,68,0.1)', border: '1px solid rgba(239,68,68,0.3)', borderRadius: 8, color: '#ef4444', fontSize: 12 }}>{saveError}</div>
        )}

        <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
          <HoloButton onClick={() => setShowModal(false)}>Cancel</HoloButton>
          <HoloButton variant="primary" onClick={() => save.mutate()} disabled={save.isPending || !canSave}>
            {save.isPending ? 'Saving…' : 'Save'}
          </HoloButton>
        </div>
      </HoloModal>
    </div>
  )
}

/* ─── Access Map tab ─────────────────────────────────────── */

// Pure function: returns set of all node IDs reachable from (type, id) in both directions.
function buildChain(g: AccessGraph, type: GNodeType, id: string): Set<string> {
  const set = new Set<string>([id])

  function roleDown(rid: string) {
    const role = g.roles.find(r => r.id === rid)
    if (!role) return
    role.privilegeIds.forEach(pid => {
      set.add(pid)
      const priv = g.privileges.find(p => p.id === pid)
      if (priv?.contentSelectorId) set.add(priv.contentSelectorId)
    })
    role.roleIds.forEach(nid => { if (!set.has(nid)) { set.add(nid); roleDown(nid) } })
  }

  function roleUp(rid: string) {
    g.roles.filter(r => r.roleIds.includes(rid)).forEach(r => {
      if (!set.has(r.id)) { set.add(r.id); roleUp(r.id) }
    })
    g.users.filter(u => u.roleIds.includes(rid)).forEach(u => set.add(u.id))
  }

  if (type === 'user') {
    const user = g.users.find(u => u.id === id)
    user?.roleIds.forEach(rid => { set.add(rid); roleDown(rid) })
  } else if (type === 'role') {
    roleDown(id); roleUp(id)
  } else if (type === 'privilege') {
    const priv = g.privileges.find(p => p.id === id)
    if (priv?.contentSelectorId) set.add(priv.contentSelectorId)
    g.roles.filter(r => r.privilegeIds.includes(id)).forEach(r => {
      set.add(r.id); roleDown(r.id)
      g.users.filter(u => u.roleIds.includes(r.id)).forEach(u => set.add(u.id))
    })
  } else {
    g.privileges.filter(p => p.contentSelectorId === id).forEach(p => {
      set.add(p.id)
      g.roles.filter(r => r.privilegeIds.includes(p.id)).forEach(r => {
        set.add(r.id)
        g.users.filter(u => u.roleIds.includes(r.id)).forEach(u => set.add(u.id))
      })
    })
  }
  return set
}

function AccessMapTab() {
  const [selType, setSelType] = useState<GNodeType|null>(null)
  const [selId,   setSelId]   = useState<string|null>(null)
  const [search,  setSearch]  = useState('')
  const [open,    setOpen]    = useState(false)

  const { data: graph, isLoading, isError } = useQuery<AccessGraph>({
    queryKey: ['access-graph'],
    queryFn: () => apiClient.get<AccessGraph>('/api/v1/security/access-graph').then(r => r.data),
    staleTime: 60_000,
  })

  // Combobox options filtered by search.
  const options = useMemo(() => {
    if (!graph || !selType) return [] as {id:string;label:string;hint:string}[]
    const q = search.toLowerCase()
    const src =
      selType === 'user'      ? graph.users.map(u => ({ id:u.id, label:u.username, hint:u.email })) :
      selType === 'role'      ? graph.roles.map(r => ({ id:r.id, label:r.name, hint:`${r.privilegeIds.length} privileges` })) :
      selType === 'privilege' ? graph.privileges.map(p => ({ id:p.id, label:p.name, hint:p.type })) :
                                graph.selectors.map(s => ({ id:s.id, label:s.name, hint:'' }))
    return src.filter(o => o.label.toLowerCase().includes(q))
  }, [graph, selType, search])

  // Chain of node IDs to render. When nothing is selected, shows admin + anonymous by default.
  const chainIds = useMemo((): Set<string> => {
    if (!graph) return new Set()
    if (selId && selType) return buildChain(graph, selType, selId)
    // Default view: union of admin + anonymous chains.
    const set = new Set<string>()
    graph.users
      .filter(u => u.username === 'admin' || u.username === 'anonymous')
      .forEach(u => buildChain(graph, 'user', u.id).forEach(id => set.add(id)))
    return set
  }, [graph, selId, selType])

  // Node center positions: only chain nodes are placed (non-chain nodes are not rendered).
  const posMap = useMemo(() => {
    const m = new Map<string, {cx:number; cy:number}>()
    if (!graph) return m
    const place = (items:{id:string}[], cx:number) =>
      items.filter(it => chainIds.has(it.id))
           .forEach((it, i) => m.set(it.id, { cx, cy: ROW_Y0 + i * ROW_GAP + NODE_H / 2 }))
    place(graph.users,      COL_CX.user)
    place(graph.roles,      COL_CX.role)
    place(graph.privileges, COL_CX.privilege)
    place(graph.selectors,  COL_CX.selector)
    return m
  }, [graph, chainIds])

  const svgH = useMemo(() => {
    if (!graph) return 200
    const max = Math.max(
      graph.users.filter(u => chainIds.has(u.id)).length,
      graph.roles.filter(r => chainIds.has(r.id)).length,
      graph.privileges.filter(p => chainIds.has(p.id)).length,
      graph.selectors.filter(s => chainIds.has(s.id)).length,
      1,
    )
    return ROW_Y0 + max * ROW_GAP + 20
  }, [graph, chainIds])

  function pick(type: GNodeType, id: string) {
    setSelType(type); setSelId(id); setSearch(''); setOpen(false)
  }
  function reset() { setSelType(null); setSelId(null); setSearch('') }

  // Pill labels/keys per type.
  const pills: {key:GNodeType; label:string}[] = [
    {key:'user', label:'User'}, {key:'role', label:'Role'},
    {key:'privilege', label:'Privilege'}, {key:'selector', label:'Content Selector'},
  ]

  // Render a single node <rect>+<text>. Only chain nodes have a posMap entry.
  function GNode({ id, label, type }: {id:string; label:string; type:GNodeType}) {
    const pos = posMap.get(id)
    if (!pos) return null
    const { stroke, fill, text } = NODE_COLORS[type]
    const active = id === selId
    const x = pos.cx - NODE_W / 2
    const y = pos.cy - NODE_H / 2
    return (
      <g style={{ cursor: 'pointer' }} onClick={() => pick(type, id)}>
        <rect x={x} y={y} width={NODE_W} height={NODE_H} rx={6}
          fill={fill} stroke={stroke} strokeWidth={active ? 2 : 1} />
        <text x={pos.cx} y={pos.cy + 4} textAnchor="middle"
          fill={text} fontSize={10} fontWeight={active ? 700 : 400}
          style={{ pointerEvents:'none', userSelect:'none' as const }}>
          {label.length > 14 ? label.slice(0, 13) + '…' : label}
        </text>
      </g>
    )
  }

  // Render an edge line between two nodes. Returns null if either end is not in the chain.
  function Edge({ fromId, toId }: {fromId:string; toId:string}) {
    const a = posMap.get(fromId), b = posMap.get(toId)
    if (!a || !b) return null
    const x1 = a.cx + NODE_W / 2, x2 = b.cx - NODE_W / 2
    return <line x1={x1} y1={a.cy} x2={x2} y2={b.cy}
      stroke="#334155" strokeWidth={1.5} markerEnd="url(#arr)" opacity={0.7} />
  }

  // Sidebar detail panel content. A render helper (not a component) — it only
  // reads closure state and returns JSX, so it's invoked, not mounted.
  function renderSidebarDetail() {
    if (!selId || !selType || !graph) return null
    const s = { fontSize:11, color:'var(--holo-text-faint)' }
    const detailLabel = (t:string) => <div style={{fontSize:9, color:'#475569', textTransform:'uppercase' as const, letterSpacing:1, marginBottom:4, marginTop:10}}>{t}</div>

    if (selType === 'user') {
      const u = graph.users.find(x => x.id === selId)
      if (!u) return null
      const roleNames = u.roleIds.map(rid => graph.roles.find(r => r.id === rid)?.name ?? rid)
      return <>
        <div style={{color:NODE_COLORS.user.text, fontSize:9, textTransform:'uppercase' as const, letterSpacing:1, marginBottom:6}}>User</div>
        <div style={{color:'#e2e8f0', fontWeight:600, marginBottom:2}}>{u.username}</div>
        <div style={s}>{u.email}</div>
        {detailLabel('Roles')}
        {roleNames.length ? roleNames.map(n => <div key={n} style={{background:'#0c2340', borderRadius:4, padding:'2px 6px', color:'#60a5fa', fontSize:10, marginBottom:2}}>{n}</div>) : <div style={s}>none</div>}
        {detailLabel('Status')} <div style={{color: u.status==='active'?'#22c55e':'#ef4444', fontSize:10}}>● {u.status}</div>
        {detailLabel('Source')} <div style={s}>{u.source}</div>
      </>
    }
    if (selType === 'role') {
      const r = graph.roles.find(x => x.id === selId)
      if (!r) return null
      return <>
        <div style={{color:NODE_COLORS.role.text, fontSize:9, textTransform:'uppercase' as const, letterSpacing:1, marginBottom:6}}>Role</div>
        <div style={{color:'#e2e8f0', fontWeight:600, marginBottom:2}}>{r.name}</div>
        {r.description && <div style={s}>{r.description}</div>}
        {detailLabel('Privileges')} <div style={s}>{r.privilegeIds.length} privilege{r.privilegeIds.length!==1?'s':''}</div>
        {r.roleIds.length > 0 && <>{detailLabel('Nested Roles')}<div style={s}>{r.roleIds.length} role{r.roleIds.length!==1?'s':''}</div></>}
      </>
    }
    if (selType === 'privilege') {
      const p = graph.privileges.find(x => x.id === selId)
      if (!p) return null
      const cs = p.contentSelectorId ? graph.selectors.find(s => s.id === p.contentSelectorId) : null
      return <>
        <div style={{color:NODE_COLORS.privilege.text, fontSize:9, textTransform:'uppercase' as const, letterSpacing:1, marginBottom:6}}>Privilege</div>
        <div style={{color:'#e2e8f0', fontWeight:600, marginBottom:2}}>{p.name}</div>
        {detailLabel('Type')} <div style={s}>{p.type}</div>
        {cs && <>{detailLabel('Content Selector')}<div style={{color:'#4ade80', fontSize:10}}>{cs.name}</div></>}
      </>
    }
    // selector
    const sel = graph.selectors.find(x => x.id === selId)
    if (!sel) return null
    return <>
      <div style={{color:NODE_COLORS.selector.text, fontSize:9, textTransform:'uppercase' as const, letterSpacing:1, marginBottom:6}}>Content Selector</div>
      <div style={{color:'#e2e8f0', fontWeight:600, marginBottom:2}}>{sel.name}</div>
      {detailLabel('Expression')}
      <div style={{fontFamily:'monospace', fontSize:10, color:'#94a3b8', background:'#0a1120', borderRadius:4, padding:'4px 6px', wordBreak:'break-all'}}>{sel.expression}</div>
    </>
  }

  const hasGraph = graph && (graph.users.length > 0 || graph.roles.length > 0 || graph.privileges.length > 0 || graph.selectors.length > 0)

  return (
    <div style={{display:'flex', flexDirection:'column', gap:12}}>
      {/* Type pills + combobox */}
      <div style={{display:'flex', gap:8, alignItems:'center', flexWrap:'wrap'}}>
        <span style={{fontSize:10, color:'#475569'}}>Type:</span>
        {pills.map(({key, label}) => (
          <div key={key} onClick={() => { setSelType(key); setSelId(null); setSearch(''); setOpen(false) }}
            style={{
              background: selType===key ? '#1e1049' : '#0a0f1a',
              border: `1px solid ${selType===key ? '#7c5cff' : '#1e3a5f'}`,
              borderRadius:5, padding:'4px 10px',
              color: selType===key ? '#a78bfa' : '#475569',
              fontSize:11, cursor:'pointer', fontWeight: selType===key ? 600 : 400,
            }}>{label}</div>
        ))}
        {selId && (
          <div onClick={reset} style={{background:'transparent', border:'1px solid #334155', borderRadius:5, padding:'4px 10px', color:'#64748b', fontSize:11, cursor:'pointer'}}>× Reset</div>
        )}
      </div>

      {selType && (
        <div style={{position:'relative', maxWidth:360}}>
          <input
            value={search}
            onChange={e => { setSearch(e.target.value); setOpen(true) }}
            onFocus={() => setOpen(true)}
            onBlur={() => setTimeout(() => setOpen(false), 150)}
            onKeyDown={(e) => e.key === 'Escape' && setOpen(false)}
            placeholder={`Search ${selType}s…`}
            style={{width:'100%', background:'#0d1829', border:`1px solid ${selType?NODE_COLORS[selType].stroke:'#1e3a5f'}`, borderRadius: open && options.length ? '6px 6px 0 0' : 6, color:'#e2e8f0', fontSize:12, padding:'7px 10px', outline:'none', boxSizing:'border-box'}}
          />
          {open && options.length > 0 && (
            <div style={{position:'absolute', zIndex:10, width:'100%', background:'#0d1829', border:`1px solid ${NODE_COLORS[selType].stroke}`, borderTop:'none', borderRadius:'0 0 6px 6px', maxHeight:200, overflowY:'auto'}}>
              {options.slice(0,20).map(o => (
                <div key={o.id} onClick={() => pick(selType!, o.id)}
                  style={{padding:'7px 12px', display:'flex', alignItems:'center', gap:8, cursor:'pointer', borderTop:'1px solid #0d1829'}}
                  onMouseEnter={e => (e.currentTarget.style.background='#1e1049')}
                  onMouseLeave={e => (e.currentTarget.style.background='transparent')}>
                  <span style={{color:'#e2e8f0', fontSize:11}}>{o.label}</span>
                  {o.hint && <span style={{color:'#475569', fontSize:10, marginLeft:'auto'}}>{o.hint}</span>}
                </div>
              ))}
            </div>
          )}
        </div>
      )}

      {/* Graph area */}
      {isLoading && <div style={{color:'#475569', fontSize:12}}>Loading graph…</div>}

      {isError && <div style={{color:'#ef4444', fontSize:12}}>Failed to load access graph.</div>}

      {!isLoading && !isError && hasGraph && chainIds.size === 0 && (
        <div style={{height:200, border:'1px dashed #1e3a5f', borderRadius:8, display:'flex', flexDirection:'column', alignItems:'center', justifyContent:'center', gap:8}}>
          <div style={{color:'#1e3a5f', fontSize:24}}>⬡</div>
          <div style={{color:'#334155', fontSize:12}}>Select a node above to explore the access graph</div>
          <div style={{color:'#1e3a5f', fontSize:10}}>User → Roles → Privileges → Content Selectors</div>
        </div>
      )}

      {!isLoading && !isError && !hasGraph && (
        <div style={{height:200, border:'1px dashed #1e3a5f', borderRadius:8, display:'flex', flexDirection:'column', alignItems:'center', justifyContent:'center', gap:8}}>
          <div style={{color:'#1e3a5f', fontSize:24}}>⬡</div>
          <div style={{color:'#334155', fontSize:12}}>No data — system has no users, roles, or privileges yet</div>
        </div>
      )}

      {!isLoading && hasGraph && graph && chainIds.size > 0 && (
        <div style={{display:'flex', gap:12, alignItems:'flex-start'}}>
          {/* SVG graph */}
          <div style={{flex:1, overflowX:'auto', background:'#070b14', borderRadius:8, border:'1px solid #1e3a5f'}}>
            <svg width={SVG_W} height={svgH} viewBox={`0 0 ${SVG_W} ${svgH}`} style={{fontFamily:'system-ui', display:'block'}}>
              <defs>
                <marker id="arr" markerWidth={6} markerHeight={6} refX={3} refY={3} orient="auto">
                  <path d="M0,0 L0,6 L6,3 z" fill="#334155"/>
                </marker>
              </defs>
              {/* Column labels */}
              {(['user','role','privilege','selector'] as GNodeType[]).map(type => (
                <text key={type} x={COL_CX[type]} y={16} textAnchor="middle"
                  fill={NODE_COLORS[type].stroke} fontSize={8} fontWeight={600} letterSpacing={1}
                  style={{textTransform:'uppercase' as const}}>
                  {type === 'selector' ? 'SELECTORS' : type.toUpperCase() + 'S'}
                </text>
              ))}
              {/* Edges */}
              {graph.users.flatMap(u => u.roleIds.map(rid => <Edge key={`u${u.id}-r${rid}`} fromId={u.id} toId={rid}/>))}
              {graph.roles.flatMap(r => [
                ...r.privilegeIds.map(pid => <Edge key={`r${r.id}-p${pid}`} fromId={r.id} toId={pid}/>),
                ...r.roleIds.map(nid => <Edge key={`r${r.id}-nr${nid}`} fromId={r.id} toId={nid}/>),
              ])}
              {graph.privileges.filter(p => p.contentSelectorId).map(p =>
                <Edge key={`p${p.id}-cs`} fromId={p.id} toId={p.contentSelectorId!}/>
              )}
              {/* Nodes */}
              {graph.users.map(u      => <GNode key={u.id}   id={u.id}   label={u.username} type="user"/>)}
              {graph.roles.map(r      => <GNode key={r.id}   id={r.id}   label={r.name}     type="role"/>)}
              {graph.privileges.map(p => <GNode key={p.id}   id={p.id}   label={p.name}     type="privilege"/>)}
              {graph.selectors.map(s  => <GNode key={s.id}   id={s.id}   label={s.name}     type="selector"/>)}
            </svg>
          </div>
          {/* Sidebar */}
          {selId && (
            <div style={{width:200, background:'#0d1829', border:'1px solid #1e3a5f', borderRadius:8, padding:14, flexShrink:0, fontSize:11}}>
              {renderSidebarDetail()}
            </div>
          )}
        </div>
      )}
    </div>
  )
}

/* ─── Main page ──────────────────────────────────────────── */
type Tab = 'roles' | 'privileges' | 'selectors' | 'users' | 'scan' | 'vulndash' | 'webhooks' | 'accessmap'

export default function SecurityPage() {
  const { isAdmin } = useAuthStore()
  const admin = isAdmin()
  const [tab, setTab] = useState<Tab>('roles')
  const { data: roles = [], isLoading, refetch } = useQuery<Role[]>({
    queryKey: ['roles'],
    queryFn: () => nexusApi.listRoles().then(r => r.data),
  })

  const allTabs: HoloTabItem[] = [
    { value: 'roles',      label: 'Roles' },
    { value: 'privileges', label: 'Privileges' },
    { value: 'selectors',  label: 'Content Selectors' },
    ...(admin ? [
      { value: 'users',    label: 'Users' },
      { value: 'scan',      label: 'CVE Scan' },
      { value: 'vulndash',  label: 'Vulnerability Dashboard' },
      { value: 'webhooks',  label: 'Webhooks' },
      { value: 'accessmap', label: 'Access Map' },
    ] : []),
  ]

  return (
    <div style={{ padding: 24, display: 'flex', flexDirection: 'column', gap: 20 }}>
      <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', flexWrap: 'wrap', gap: 12 }}>
        <div>
          <div className="holo-section-label" style={{ marginBottom: 4 }}>ADMINISTRATION / SECURITY</div>
          <h1 style={{ fontSize: 20, fontWeight: 700, margin: '0 0 3px', letterSpacing: '-0.01em', lineHeight: 1.2, background: 'linear-gradient(110deg, #7c5cff, #22d3ee 60%)', WebkitBackgroundClip: 'text', WebkitTextFillColor: 'transparent', backgroundClip: 'text' as const }}>Security</h1>
          <p style={{ fontSize: 12, color: 'var(--holo-text-faint)', margin: 0 }}>
            {admin
              ? 'Roles, users, privileges, content selectors and webhooks'
              : 'View roles and privileges. Content Selector → Privilege → Role defines what each user can access.'}
          </p>
        </div>
        {tab === 'roles' && (
          <HoloButton onClick={() => refetch()} title="Refresh"><RefreshCw size={16} /></HoloButton>
        )}
      </div>

      <HoloTabs items={allTabs} value={tab} onChange={v => setTab(v as Tab)} />

      <div style={{ marginTop: 4 }}>
        {tab === 'roles'      && <RolesTab roles={roles} loading={isLoading} onRefresh={refetch} admin={admin} />}
        {tab === 'privileges' && <PrivilegesTab admin={admin} />}
        {tab === 'selectors'  && <ContentSelectorsTab admin={admin} />}
        {tab === 'users'      && admin && <UsersTab />}
        {tab === 'scan'       && admin && <ScanTab />}
        {tab === 'vulndash'   && admin && <VulnDashTab />}
        {tab === 'webhooks'   && admin && <WebhooksTab />}
        {tab === 'accessmap'  && admin && <AccessMapTab />}
      </div>
    </div>
  )
}
