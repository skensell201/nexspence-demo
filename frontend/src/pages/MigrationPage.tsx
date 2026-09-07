import { useRef, useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { ArrowRightLeft, Play, Pause, RefreshCw, Plus, PlugZap } from 'lucide-react'
import { nexspenceApi, apiErrorMessage } from '@/api/client'
import { HoloCard, HoloButton, HoloPill, HoloInput, HoloModal } from '@/components/holo'
import { type MigrationJob, shouldPollJobs } from './migrationJobs'

const STATUS_STYLE: Record<string, { bg: string; color: string }> = {
  pending:   { bg: 'rgba(245,158,11,0.15)',  color: '#f59e0b' },
  running:   { bg: 'rgba(59,130,246,0.15)',  color: '#3b82f6' },
  paused:    { bg: 'rgba(107,114,128,0.15)', color: '#9ca3af' },
  done:      { bg: 'rgba(34,197,94,0.15)',   color: '#22c55e' },
  error:     { bg: 'rgba(239,68,68,0.15)',   color: '#ef4444' },
  // Kept for jobs recorded by older builds, which used these two labels.
  completed: { bg: 'rgba(34,197,94,0.15)',   color: '#22c55e' },
  failed:    { bg: 'rgba(239,68,68,0.15)',   color: '#ef4444' },
}

export default function MigrationPage() {
  const qc = useQueryClient()
  const [showCreate, setShowCreate] = useState(false)

  const { data: jobs = [], isLoading, refetch } = useQuery<MigrationJob[]>({
    queryKey: ['migrationJobs'],
    queryFn: () => nexspenceApi.listMigrationJobs().then(r => r.data),
    refetchInterval: (q) => shouldPollJobs(q.state.data as MigrationJob[] | undefined),
  })

  const pauseMut = useMutation({
    mutationFn: (id: string) => nexspenceApi.pauseMigrationJob(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['migrationJobs'] }),
  })
  const resumeMut = useMutation({
    mutationFn: (id: string) => nexspenceApi.resumeMigrationJob(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['migrationJobs'] }),
  })

  return (
    <div style={{ padding: 24, display: 'flex', flexDirection: 'column', gap: 24 }}>
      <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', gap: 16 }}>
        <div>
          <div className="holo-section-label" style={{ marginBottom: 4 }}>ADMINISTRATION / MIGRATION</div>
          <h1 style={{ fontSize: 20, fontWeight: 700, margin: '0 0 3px', letterSpacing: '-0.01em', lineHeight: 1.2, background: 'linear-gradient(110deg, #7c5cff, #22d3ee 60%)', WebkitBackgroundClip: 'text', WebkitTextFillColor: 'transparent', backgroundClip: 'text' as const }}>Migration from Nexus</h1>
          <p style={{ fontSize: 12, color: 'var(--holo-text-faint)', margin: 0 }}>
            Import repositories, users, and artifacts from a live Nexus instance
          </p>
        </div>
        <div style={{ display: 'flex', gap: 10, alignItems: 'center' }}>
          <HoloButton onClick={() => refetch()} aria-label="Refresh"><RefreshCw size={16} /></HoloButton>
          <HoloButton variant="primary" onClick={() => setShowCreate(true)}><Plus size={16} /> New Migration</HoloButton>
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
      ) : jobs.length === 0 ? (
        <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: 8, color: 'var(--holo-text-faint)', fontSize: 14, padding: '48px 0' }}>
          <ArrowRightLeft size={40} style={{ opacity: 0.3 }} />
          <div style={{ fontWeight: 500, color: 'var(--holo-text)' }}>No migration jobs yet</div>
          <div style={{ fontSize: 12 }}>Import repositories, users, and artifacts from a live Nexus instance.</div>
          <HoloButton variant="primary" style={{ marginTop: 4 }} onClick={() => setShowCreate(true)}><Plus size={14} /> Start Migration</HoloButton>
        </div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
          {jobs.map(job => {
            const reposPct = job.repositoriesTotal ? Math.round((job.repositoriesDone / job.repositoriesTotal) * 100) : 0
            const assetsPct = job.assetsTotal ? Math.round((job.assetsDone / job.assetsTotal) * 100) : 0
            return (
              <HoloCard key={job.id} style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
                  <ArrowRightLeft size={15} style={{ color: 'var(--holo-text-faint)', flexShrink: 0 }} />
                  <span style={{ flex: 1, fontSize: 14, fontWeight: 600, color: 'var(--holo-text)', fontFamily: 'monospace', wordBreak: 'break-all' }}>{job.sourceUrl}</span>
                  <HoloPill style={{ background: STATUS_STYLE[job.status]?.bg ?? STATUS_STYLE.pending.bg, color: STATUS_STYLE[job.status]?.color ?? STATUS_STYLE.pending.color, fontSize: 11, fontWeight: 600 }}>{job.status}</HoloPill>
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

                {job.lastError && (
                  <div style={{ background: 'rgba(239,68,68,0.08)', border: '1px solid rgba(239,68,68,0.2)', borderRadius: 8, padding: '8px 12px', fontSize: 12, color: '#fca5a5', wordBreak: 'break-word' }}>
                    {job.lastError}
                  </div>
                )}

                <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
                  <span style={{ fontSize: 12, color: 'var(--holo-text-faint)' }}>Started {new Date(job.createdAt).toLocaleString()}</span>
                  <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
                    {job.status === 'running' && (
                      <HoloButton onClick={() => pauseMut.mutate(job.id)}><Pause size={12} /> Pause</HoloButton>
                    )}
                    {job.status === 'paused' && (
                      <HoloButton onClick={() => resumeMut.mutate(job.id)}><Play size={12} /> Resume</HoloButton>
                    )}
                  </div>
                </div>
              </HoloCard>
            )
          })}
        </div>
      )}

      {showCreate && (
        <CreateMigrationModal
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

interface PreviewRepo { name: string; format: string; type: string }
interface PreviewResult { reachable: boolean; repoCount: number; repos: PreviewRepo[] }

/** The stages a job can run, in the order the engine runs them. */
const SCOPES = [
  { key: 'migrateRepos',        label: 'Repositories' },
  { key: 'migrateBlobs',        label: 'Artifacts' },
  { key: 'migratePrivileges',   label: 'Privileges' },
  { key: 'migrateRoles',        label: 'Roles' },
  { key: 'migrateUsers',        label: 'Users' },
  { key: 'migrateRoutingRules', label: 'Routing rules' },
] as const

type ScopeKey = (typeof SCOPES)[number]['key']

/** Source realms user migration can pull accounts from ("default" = local).
 *  Local-only by default: an external account migrated onto a fresh target is
 *  a permanently-unusable login unless its provider is already configured. */
const USER_REALMS = [
  { key: 'default', label: 'Local' },
  { key: 'LDAP', label: 'LDAP' },
  { key: 'OIDC', label: 'OIDC' },
  { key: 'SAML', label: 'SAML' },
] as const

function CreateMigrationModal({ onClose, onCreated }: { onClose: () => void; onCreated: () => void }) {
  const [form, setForm] = useState({ sourceUrl: '', username: 'admin', password: '' })
  const [scope, setScope] = useState<Record<ScopeKey, boolean>>(
    () => Object.fromEntries(SCOPES.map(s => [s.key, true])) as Record<ScopeKey, boolean>,
  )
  const [userRealms, setUserRealms] = useState<string[]>(['default'])
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const [testing, setTesting] = useState(false)
  const [preview, setPreview] = useState<PreviewResult | null>(null)
  const [previewError, setPreviewError] = useState('')
  // Bumped by handleTest and by every field set() feeds into it. A field
  // edited after a test started must not let that test's late response land
  // as if it verified the (now different) details a moment later — same
  // request-sequence-guard pattern issue 12 applies to SecurityPage.tsx's
  // openEdit.
  const testReqSeq = useRef(0)

  const set = (k: keyof typeof form) => (e: React.ChangeEvent<HTMLInputElement>) => {
    // A connection result belongs to the details it was tested with. Once any
    // of them changes it is stale, and a stale green banner is worse than none.
    testReqSeq.current++
    setPreview(null)
    setPreviewError('')
    setForm(f => ({ ...f, [k]: e.target.value }))
  }

  const toggleScope = (k: ScopeKey) => () => setScope(s => ({ ...s, [k]: !s[k] }))

  const toggleRealm = (realm: string) => () =>
    setUserRealms(r => (r.includes(realm) ? r.filter(x => x !== realm) : [...r, realm]))

  const handleTest = async () => {
    const seq = ++testReqSeq.current
    setPreview(null)
    setPreviewError('')
    setTesting(true)
    try {
      const { data } = await nexspenceApi.previewMigration({
        sourceUrl: form.sourceUrl,
        username: form.username,
        password: form.password,
      })
      if (seq !== testReqSeq.current) return // a field changed since this test started
      setPreview(data as PreviewResult)
    } catch (err) {
      if (seq !== testReqSeq.current) return
      setPreviewError(apiErrorMessage(err, 'Could not reach that Nexus'))
    } finally {
      // Unlike setPreview/setPreviewError above, this must run regardless of
      // seq: nothing else ever clears testing for a superseded request (no
      // new test was started), so gating it the same way would leave the
      // button stuck showing "Testing…" forever after an edit invalidates an
      // in-flight one.
      setTesting(false)
    }
  }

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    setError('')
    setLoading(true)
    try {
      await nexspenceApi.createMigrationJob({
        sourceUrl: form.sourceUrl,
        credentials: { username: form.username, password: form.password },
        scope: { ...scope, userRealms },
      })
      onCreated()
    } catch (err) {
      setError(apiErrorMessage(err, 'Failed to create migration job'))
    } finally {
      setLoading(false)
    }
  }

  return (
    <HoloModal open={true} onClose={onClose}>
      <h2 style={{ fontSize: 18, fontWeight: 700, color: 'var(--holo-text)', margin: 0 }}>New Migration Job</h2>
      <form style={{ display: 'flex', flexDirection: 'column', gap: 14 }} onSubmit={handleSubmit}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 5 }}>
          <label style={{ fontSize: 11, fontWeight: 600, color: 'var(--holo-text-dim)', textTransform: 'uppercase', letterSpacing: '0.04em' }}>Nexus URL *</label>
          <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
            <HoloInput style={{ flex: 1 }} placeholder="https://nexus.example.com" value={form.sourceUrl} onChange={set('sourceUrl')} required />
            <HoloButton type="button" onClick={handleTest} disabled={testing || !form.sourceUrl}>
              <PlugZap size={14} /> {testing ? 'Testing…' : 'Test connection'}
            </HoloButton>
          </div>
        </div>

        {preview && (
          <div style={{ background: 'rgba(34,197,94,0.1)', border: '1px solid rgba(34,197,94,0.25)', borderRadius: 8, padding: '10px 12px', color: '#86efac', fontSize: 13 }}>
            <div style={{ fontWeight: 600 }}>
              Connected — {preview.repoCount} {preview.repoCount === 1 ? 'repository' : 'repositories'} found
            </div>
            {preview.repos.length > 0 && (
              <div style={{ marginTop: 6, fontFamily: 'monospace', fontSize: 12, maxHeight: 120, overflowY: 'auto', lineHeight: 1.6 }}>
                {preview.repos.map(r => (
                  <div key={r.name}>{r.name} <span style={{ opacity: 0.7 }}>({r.format}/{r.type})</span></div>
                ))}
              </div>
            )}
          </div>
        )}
        {previewError && (
          <div style={{ background: 'rgba(239,68,68,0.1)', border: '1px solid rgba(239,68,68,0.25)', borderRadius: 8, padding: '10px 12px', color: '#fca5a5', fontSize: 13, wordBreak: 'break-word' }}>
            {previewError}
          </div>
        )}
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 5 }}>
            <label style={{ fontSize: 11, fontWeight: 600, color: 'var(--holo-text-dim)', textTransform: 'uppercase', letterSpacing: '0.04em' }}>Username</label>
            <HoloInput value={form.username} onChange={set('username')} />
          </div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 5 }}>
            <label style={{ fontSize: 11, fontWeight: 600, color: 'var(--holo-text-dim)', textTransform: 'uppercase', letterSpacing: '0.04em' }}>Password *</label>
            <HoloInput type="password" value={form.password} onChange={set('password')} required />
          </div>
        </div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
          <label style={{ fontSize: 11, fontWeight: 600, color: 'var(--holo-text-dim)', textTransform: 'uppercase', letterSpacing: '0.04em' }}>What to migrate</label>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '6px 12px' }}>
            {SCOPES.map(s => (
              <label key={s.key} style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13, color: 'var(--holo-text)', cursor: 'pointer' }}>
                <input type="checkbox" checked={scope[s.key]} onChange={toggleScope(s.key)} />
                {s.label}
              </label>
            ))}
          </div>
        </div>
        {scope.migrateUsers && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
            <label style={{ fontSize: 11, fontWeight: 600, color: 'var(--holo-text-dim)', textTransform: 'uppercase', letterSpacing: '0.04em' }}>User realms</label>
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '6px 12px' }}>
              {USER_REALMS.map(r => (
                <label key={r.key} style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13, color: 'var(--holo-text)', cursor: 'pointer' }}>
                  <input type="checkbox" checked={userRealms.includes(r.key)} onChange={toggleRealm(r.key)} />
                  {r.label}
                </label>
              ))}
            </div>
            <span style={{ fontSize: 11, color: 'var(--holo-text-faint)' }}>
              An external realm (LDAP/OIDC/SAML) only makes sense when the matching provider is already configured here.
            </span>
          </div>
        )}
        {error && <div style={{ background: 'rgba(239,68,68,0.1)', border: '1px solid rgba(239,68,68,0.25)', borderRadius: 8, padding: '10px 12px', color: '#fca5a5', fontSize: 13 }}>{error}</div>}
        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 10, marginTop: 8 }}>
          <HoloButton type="button" onClick={onClose}>Cancel</HoloButton>
          <HoloButton type="submit" variant="primary" disabled={loading}>{loading ? 'Starting…' : 'Start Migration'}</HoloButton>
        </div>
      </form>
    </HoloModal>
  )
}
