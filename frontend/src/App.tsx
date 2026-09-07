import { useEffect, Suspense, lazy, Component } from 'react'
import type { ReactNode, ErrorInfo } from 'react'
import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom'
import Layout from '@/components/Layout'
import LoginPage from '@/pages/LoginPage'
import RepositoriesPage from '@/pages/RepositoriesPage'
import OIDCCallbackPage from '@/pages/OIDCCallbackPage'
import SAMLCallbackPage from '@/pages/SAMLCallbackPage'
import { useAuthStore } from '@/store/authStore'

// Lazy-load large page components for code splitting
const BrowsePage = lazy(() => import('@/pages/BrowsePage'))
const SearchPage = lazy(() => import('@/pages/SearchPage'))
const UsersPage = lazy(() => import('@/pages/UsersPage'))
const CleanupPage = lazy(() => import('@/pages/CleanupPage'))
const AdminPage = lazy(() => import('@/pages/AdminPage'))
const SecurityPage = lazy(() => import('@/pages/SecurityPage'))
const AuditPage = lazy(() => import('@/pages/AuditPage'))
const DocsPage = lazy(() => import('@/pages/DocsPage'))

class ErrorBoundary extends Component<{ children: ReactNode }, { error: Error | null }> {
  state = { error: null }
  static getDerivedStateFromError(error: Error) { return { error } }
  componentDidCatch(error: Error, _info: ErrorInfo) { console.error('[ErrorBoundary]', error) }
  render() {
    if (this.state.error) {
      const msg = (this.state.error as Error).message
      return (
        <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', height: '100vh', gap: 12, color: '#64748b', fontSize: 14 }}>
          <span style={{ fontSize: 24 }}>⚠</span>
          <span>Something went wrong loading this page.</span>
          {msg && <code style={{ fontSize: 11, background: 'rgba(255,255,255,0.05)', padding: '4px 10px', borderRadius: 4, color: '#ef4444', maxWidth: 500, textAlign: 'center' as const, wordBreak: 'break-all' as const }}>{msg}</code>}
          <button onClick={() => { this.setState({ error: null }); window.history.back() }} style={{ marginTop: 8, padding: '6px 16px', borderRadius: 6, border: '1px solid rgba(255,255,255,0.15)', background: 'rgba(255,255,255,0.06)', color: '#94a3b8', cursor: 'pointer', fontSize: 13 }}>
            Go back
          </button>
        </div>
      )
    }
    return this.props.children
  }
}

function PageSkeleton() {
  return (
    <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', height: '100vh', color: '#64748b', fontSize: '14px' }}>
      Loading…
    </div>
  )
}

function PrivateRoute({ children }: { children: React.ReactNode }) {
  const token = useAuthStore(s => s.token)
  if (!token) return <Navigate to="/login" replace />
  return <>{children}</>
}

// Repositories, Browse, Search and Docs render for a signed-out visitor too:
// the read endpoints behind them answer anonymously, and the server returns
// only repositories with allow_anonymous (and only while
// auth.anonymous_enabled is on), so a closed instance shows an empty list
// instead of a login wall (#404). Everything else stays behind PrivateRoute.

export default function App() {
  const init = useAuthStore(s => s.init)
  useEffect(() => { init() }, [init])

  return (
    <BrowserRouter>
      <Routes>
        <Route path="/login" element={<LoginPage />} />
        <Route path="/oidc/callback" element={<OIDCCallbackPage />} />
        <Route path="/saml/callback" element={<SAMLCallbackPage />} />
        <Route path="/" element={<Layout />}>
          <Route index element={<Navigate to="/repositories" replace />} />
          <Route path="repositories" element={<RepositoriesPage />} />
          <Route
            path="browse"
            element={
              <ErrorBoundary><Suspense fallback={<PageSkeleton />}>
                <BrowsePage />
              </Suspense></ErrorBoundary>
            }
          />
          <Route
            path="search"
            element={
              <ErrorBoundary><Suspense fallback={<PageSkeleton />}>
                <SearchPage />
              </Suspense></ErrorBoundary>
            }
          />
          <Route
            path="users"
            element={
              <PrivateRoute><ErrorBoundary><Suspense fallback={<PageSkeleton />}>
                <UsersPage />
              </Suspense></ErrorBoundary></PrivateRoute>
            }
          />
          <Route
            path="cleanup"
            element={
              <PrivateRoute><ErrorBoundary><Suspense fallback={<PageSkeleton />}>
                <CleanupPage />
              </Suspense></ErrorBoundary></PrivateRoute>
            }
          />
          <Route
            path="admin"
            element={
              <PrivateRoute><ErrorBoundary><Suspense fallback={<PageSkeleton />}>
                <AdminPage />
              </Suspense></ErrorBoundary></PrivateRoute>
            }
          />
          <Route path="migration" element={<Navigate to="/admin?tab=migration" replace />} />
          <Route
            path="security"
            element={
              <PrivateRoute><ErrorBoundary><Suspense fallback={<PageSkeleton />}>
                <SecurityPage />
              </Suspense></ErrorBoundary></PrivateRoute>
            }
          />
          <Route
            path="audit"
            element={
              <PrivateRoute><ErrorBoundary><Suspense fallback={<PageSkeleton />}>
                <AuditPage />
              </Suspense></ErrorBoundary></PrivateRoute>
            }
          />
          <Route path="monitoring" element={<Navigate to="/admin?tab=monitoring" replace />} />
          <Route
            path="docs"
            element={
              <ErrorBoundary><Suspense fallback={<PageSkeleton />}>
                <DocsPage />
              </Suspense></ErrorBoundary>
            }
          />
        </Route>
      </Routes>
    </BrowserRouter>
  )
}
