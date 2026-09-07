import { useState } from 'react'
import { Outlet, NavLink } from 'react-router-dom'
import {
  Home, Search, FolderOpen, Trash2,
  Settings, Shield, FileText, LogOut,
  Key, LogIn, Plus, X, Copy, Check,
  BookOpen, ChevronLeft, ChevronRight,
} from 'lucide-react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import styles from './Layout.module.css'
import { useAuthStore } from '@/store/authStore'
import { apiClient } from '@/api/client'
import logo from '@/assets/logo.png'
import miniLogo from '@/assets/mini_logo.png'
import { HoloApp, HoloModal, HoloButton, HoloInput } from '@/components/holo'
import { Truncated } from '@/components/Truncated'

const navItems = [
  { to: '/repositories', icon: Home,       label: 'Repositories' },
  { to: '/browse',       icon: FolderOpen,  label: 'Browse' },
  { to: '/search',       icon: Search,      label: 'Search' },
]

const systemItems = [
  { to: '/security',   icon: Shield,    label: 'Security' },
  { to: '/admin',      icon: Settings,  label: 'System Admin' },
  { to: '/audit',      icon: FileText,  label: 'Audit Log' },
  { to: '/cleanup',    icon: Trash2,    label: 'Cleanup Policies' },
]

interface UserToken { id: string; name: string; createdAt: string; lastUsedAt?: string; expiresAt?: string }
interface NewToken  { id: string; name: string; token: string }

function ProfileModal({ onClose }: { onClose: () => void }) {
  const qc = useQueryClient()
  const user = useAuthStore(s => s.user)

  const { data: tokens = [], isLoading } = useQuery<UserToken[]>({
    queryKey: ['my-tokens'],
    queryFn: () => apiClient.get<UserToken[]>('/api/v1/tokens').then(r => r.data ?? []),
  })
  const { data: tokenPolicy } = useQuery<{ tokenMaxDays: number }>({
    queryKey: ['token-policy'],
    queryFn: () => apiClient.get<{ tokenMaxDays: number }>('/api/v1/auth/token-policy').then(r => r.data),
    staleTime: Infinity,
  })
  const maxDays = tokenPolicy?.tokenMaxDays ?? 90

  const [name, setName] = useState('')
  const [expiryDays, setExpiryDays] = useState('')
  const [newToken, setNewToken] = useState<NewToken | null>(null)
  const [creating, setCreating] = useState(false)
  const [copied, setCopied] = useState(false)

  const expiryError = (() => {
    if (!expiryDays) return ''
    const d = parseInt(expiryDays, 10)
    if (isNaN(d) || d < 1) return 'Expiry must be at least 1 day'
    if (d > maxDays) return `Expiry exceeds the maximum of ${maxDays} days`
    return ''
  })()

  async function create() {
    if (!name.trim()) return
    setCreating(true)
    try {
      const payload: Record<string, unknown> = { name: name.trim() }
      const days = parseInt(expiryDays, 10)
      if (expiryDays && !isNaN(days) && days > 0) payload.expiresInDays = days
      const res = await apiClient.post<NewToken>('/api/v1/tokens', payload)
      setNewToken(res.data)
      setName('')
      setExpiryDays('')
      setCopied(false)
      qc.invalidateQueries({ queryKey: ['my-tokens'] })
    } finally { setCreating(false) }
  }

  function copyToken() {
    if (!newToken) return
    void navigator.clipboard.writeText(newToken.token).then(() => {
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    })
  }

  const del = useMutation({
    mutationFn: (id: string) => apiClient.delete(`/api/v1/tokens/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['my-tokens'] }),
  })

  const S = {
    header:    { display: 'flex', alignItems: 'center', justifyContent: 'space-between' },
    title:     { fontSize: 16, fontWeight: 700, color: 'var(--holo-text)', display: 'flex', alignItems: 'center', gap: 8 },
    tokenList: { maxHeight: 240, overflowY: 'auto' as const, display: 'flex', flexDirection: 'column' as const },
    row:       { display: 'flex', alignItems: 'flex-start', gap: 10, padding: '10px 0', borderBottom: '1px solid rgba(255,255,255,0.06)', minWidth: 0 },
    rowMeta:   { flex: 1, minWidth: 0 },
    rowName:   { fontWeight: 600, fontSize: 13, color: 'var(--holo-text)' },
    rowDates:  { fontSize: 11, color: 'var(--holo-text-dim)', marginTop: 2, lineHeight: 1.4 },
    empty:     { color: 'var(--holo-text-dim)', fontSize: 13, padding: '12px 0' },
    mono:      { fontFamily: 'ui-monospace,monospace' },
  }

  return (
    <HoloModal open={true} onClose={onClose}>
      <div style={S.header}>
        <div style={S.title}>
          <Key size={16} style={{ color: 'var(--holo-a)' }} />
          Profile — {user?.username}
        </div>
        <button style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'var(--holo-text-dim)', padding: 4, display: 'flex' }} onClick={onClose}><X size={18} /></button>
      </div>

      <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
        <div className="holo-card" style={{ padding: 16 }}>
          <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--holo-text)', marginBottom: 10 }}>Create API Token</div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 8, marginBottom: 8 }}>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
              <label htmlFor="token-name" style={{ fontSize: 12, color: 'var(--holo-text-faint)' }}>Token name</label>
              <HoloInput
                id="token-name"
                placeholder="e.g. ci-deploy"
                value={name}
                onChange={e => setName(e.target.value)}
                onKeyDown={e => e.key === 'Enter' && !expiryError && create()}
              />
            </div>
            <div style={{ display: 'flex', gap: 8, alignItems: 'flex-end' }}>
              <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
                <label htmlFor="expiry-days" style={{ fontSize: 12, color: 'var(--holo-text-faint)' }}>
                  Expiry (days, optional)
                </label>
                <HoloInput
                  id="expiry-days"
                  type="number"
                  min={1}
                  max={maxDays}
                  style={{ width: 160, borderColor: expiryError ? 'rgba(255,107,107,0.6)' : undefined }}
                  placeholder={`max ${maxDays}`}
                  value={expiryDays}
                  onChange={e => setExpiryDays(e.target.value)}
                  title={`Leave empty for no expiry. Maximum ${maxDays} days.`}
                />
              </div>
              <HoloButton
                variant="default"
                icon={<Plus size={14} />}
                onClick={create}
                disabled={creating || !name.trim() || !!expiryError}
                style={{ background: 'rgba(59,130,246,0.15)', borderColor: 'rgba(59,130,246,0.4)', color: '#60a5fa', whiteSpace: 'nowrap' }}
              >
                {creating ? 'Creating…' : 'Create token'}
              </HoloButton>
            </div>
          </div>
          {expiryError
            ? <div style={{ fontSize: 11, color: 'var(--holo-red)' }}>{expiryError}</div>
            : <div style={{ fontSize: 11, color: 'var(--holo-text-faint)' }}>
                Expiry is optional — leave blank for a non-expiring token (max {maxDays} days)
              </div>
          }
        </div>

        {newToken && (
          <div className="holo-card" style={{ padding: 16, background: 'rgba(94,255,184,0.08)', border: '1px solid rgba(94,255,184,0.25)' }}>
            <div style={{ fontSize: 13, color: 'var(--holo-green)', fontWeight: 600, marginBottom: 8 }}>
              Token created — copy it now, it won't be shown again
            </div>
            <code style={{ ...S.mono, fontSize: 12, background: 'rgba(0,0,0,0.3)', padding: '8px 12px', borderRadius: 8, display: 'block', wordBreak: 'break-all' as const, color: 'var(--holo-a)' }}>
              {newToken.token}
            </code>
            <div style={{ display: 'flex', gap: 8, marginTop: 8 }}>
              <HoloButton
                variant="primary"
                icon={copied ? <Check size={14} /> : <Copy size={14} />}
                onClick={copyToken}
                style={copied ? { background: 'rgba(34,211,238,0.2)', borderColor: 'rgba(34,211,238,0.4)', color: '#22d3ee' } : undefined}
              >
                {copied ? 'Copied!' : 'Copy'}
              </HoloButton>
              <HoloButton onClick={() => setNewToken(null)}>Dismiss</HoloButton>
            </div>
          </div>
        )}

        <div className="holo-card" style={{ padding: 16 }}>
          <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--holo-text)', marginBottom: 10 }}>
            Your API Tokens {tokens.length > 0 && <span style={{ fontWeight: 400, color: 'var(--holo-text-faint)', fontSize: 12 }}>({tokens.length})</span>}
          </div>
          {isLoading
            ? <div style={S.empty}>Loading…</div>
            : tokens.length === 0
              ? <div style={S.empty}>No tokens yet</div>
              : (
                <div style={S.tokenList}>
                  {tokens.map(t => (
                    <div key={t.id} style={S.row}>
                      <Key size={13} style={{ color: 'var(--holo-a)', flexShrink: 0, marginTop: 2 }} />
                      <div style={S.rowMeta}>
                        <Truncated text={t.name} style={S.rowName} />
                        <div style={S.rowDates}>
                          Created {new Date(t.createdAt).toLocaleDateString()}
                          {t.lastUsedAt && ` · Last used ${new Date(t.lastUsedAt).toLocaleDateString()}`}
                          {t.expiresAt && ` · Expires ${new Date(t.expiresAt).toLocaleDateString()}`}
                        </div>
                      </div>
                      <HoloButton variant="danger" icon={<Trash2 size={13} />} onClick={() => del.mutate(t.id)} aria-label="Delete token" style={{ flexShrink: 0 }} />
                    </div>
                  ))}
                </div>
              )
          }
        </div>
      </div>
    </HoloModal>
  )
}

export default function Layout() {
  const { user, logout, isAdmin, isOIDC } = useAuthStore()
  const admin = isAdmin()
  const [profileOpen, setProfileOpen] = useState(false)
  const [collapsed, setCollapsed] = useState(() =>
    localStorage.getItem('sidebar-collapsed') === 'true'
  )

  const { data: systemInfo } = useQuery<{ version: string }>({
    queryKey: ['system-info'],
    queryFn: () => apiClient.get<{ version: string }>('/api/v1/system/info').then(r => r.data),
    staleTime: Infinity,
    enabled: admin,
  })

  function toggleCollapse() {
    const next = !collapsed
    setCollapsed(next)
    localStorage.setItem('sidebar-collapsed', String(next))
  }

  return (
    <HoloApp>
    <div className={`${styles.root}${collapsed ? ' ' + styles.collapsed : ''}`}>
      <a href="#main-content" className={styles.skipLink}>Skip to content</a>
      <aside className={styles.sidebar}>
        <div className={styles.navScrollArea}>
          {/* Brand */}
          <div className={styles.brand}>
            <img
              src={collapsed ? miniLogo : logo}
              alt="Nexspence"
              className={collapsed ? styles.brandLogoMini : styles.brandLogo}
            />
          </div>

          {/* Primary nav */}
          <span className={styles.sectionLabel}>Browse</span>
          <nav className={styles.nav}>
            {navItems.map(({ to, icon: Icon, label }) => (
              <NavLink
                key={to}
                to={to}
                title={collapsed ? label : undefined}
                className={({ isActive }) =>
                  `${styles.navBtn} ${isActive ? styles.active : ''}`
                }
              >
                <Icon size={16} />
                <span className={styles.navLabel}>{label}</span>
              </NavLink>
            ))}
          </nav>

          {admin && (
            <>
              <hr className={styles.divider} />
              <span className={styles.sectionLabel}>System</span>
              <nav className={styles.nav}>
                {systemItems.map(({ to, icon: Icon, label }) => (
                  <NavLink
                    key={to}
                    to={to}
                    title={collapsed ? label : undefined}
                    className={({ isActive }) =>
                      `${styles.navBtn} ${isActive ? styles.active : ''}`
                    }
                  >
                    <Icon size={16} />
                    <span className={styles.navLabel}>{label}</span>
                  </NavLink>
                ))}
              </nav>
            </>
          )}

          {/* Docs */}
          <hr className={styles.divider} />
          <span className={`${styles.sectionLabel} ${styles.sectionLabelDocs}`}>Docs</span>
          <nav className={styles.nav}>
            <NavLink
              to="/docs"
              title={collapsed ? 'Documentation' : undefined}
              className={({ isActive }) =>
                `${styles.navBtn} ${styles.navBtnDocs}${isActive ? ' ' + styles.active : ''}`
              }
            >
              <BookOpen size={16} />
              <span className={styles.navLabel}>Documentation</span>
            </NavLink>
          </nav>
        </div>

        {/* Command bar — pill */}
        {user && (
          <div className={styles.commandBar}>
            <button
              className={styles.commandBarAvatar}
              onClick={() => setProfileOpen(true)}
              title="Profile"
            >
              {(user.firstName || user.username || '?')[0].toUpperCase()}
            </button>
            <div className={styles.commandBarUser}>
              <Truncated as="span" className={styles.commandBarUserName} text={user.firstName || user.username} />
            </div>
            <button
              className={styles.commandBarAction}
              onClick={() => setProfileOpen(true)}
              title="API Tokens & Profile"
            >
              <Key size={11} />
            </button>
            <button
              className={`${styles.commandBarAction} ${styles.commandBarActionDanger}`}
              title="Sign Out"
              onClick={async () => {
                if (isOIDC()) {
                  try {
                    const res = await apiClient.get('/api/v1/auth/oidc/logout')
                    logout()
                    window.location.href = res.data.logout_url
                  } catch {
                    logout()
                  }
                } else {
                  logout()
                }
              }}
            >
              <LogOut size={11} />
            </button>
          </div>
        )}
        {/* A signed-out visitor browsing public repositories still needs a way
            in — the pill would otherwise be missing entirely (#404). */}
        {!user && (
          <div className={styles.commandBar}>
            <NavLink to="/login" className={styles.commandBarAvatar} title="Sign in">
              <LogIn size={13} />
            </NavLink>
            <div className={styles.commandBarUser}>
              <Truncated as="span" className={styles.commandBarUserName} text="Not signed in" />
            </div>
            <NavLink to="/login" className={styles.commandBarAction} title="Sign in">
              <Key size={11} />
            </NavLink>
          </div>
        )}
        <span className={styles.version}>Nexspence v{systemInfo?.version ?? '…'}</span>
        <button
          className={styles.collapseButton}
          onClick={toggleCollapse}
          title={collapsed ? 'Expand sidebar' : 'Collapse sidebar'}
        >
          {collapsed ? <ChevronRight size={13} /> : <ChevronLeft size={13} />}
        </button>
      </aside>

      <main id="main-content" className={styles.main}>
        <Outlet />
      </main>

      {profileOpen && <ProfileModal onClose={() => setProfileOpen(false)} />}
    </div>
    </HoloApp>
  )
}
