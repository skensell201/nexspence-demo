// frontend/src/api/client.test.ts
import { describe, it, expect, beforeEach } from 'vitest'
import {
  apiErrorMessage,
  apiClient,
  nexusApi,
  nexspenceApi,
  startBlobStoreMigration,
  getBlobStoreMigration,
  cancelBlobStoreMigration,
} from './client'
import { server } from '@/test/msw/server'
import { http, HttpResponse } from 'msw'

beforeEach(() => {
  localStorage.clear()
  // Reset href to a clean base URL between tests
  window.location.href = 'http://localhost/'
})

describe('apiErrorMessage', () => {
  it('returns backend error message when present', () => {
    const err = { response: { data: { error: 'not found' } } }
    expect(apiErrorMessage(err, 'fallback')).toBe('not found')
  })

  it('returns axios message when no response.data.error', () => {
    const err = { message: 'Network Error' }
    expect(apiErrorMessage(err, 'fallback')).toBe('Network Error')
  })

  it('returns fallback when error has no message and no response', () => {
    expect(apiErrorMessage({}, 'fallback')).toBe('fallback')
  })

  it('returns fallback for null input', () => {
    expect(apiErrorMessage(null, 'fallback')).toBe('fallback')
  })

  it('returns fallback for undefined input', () => {
    expect(apiErrorMessage(undefined, 'fallback')).toBe('fallback')
  })

  it('prefers response.data.error over axios message', () => {
    const err = {
      response: { data: { error: 'server error' } },
      message: 'Request failed with status 500',
    }
    expect(apiErrorMessage(err, 'fallback')).toBe('server error')
  })
})

describe('apiClient request interceptor', () => {
  it('attaches Authorization Bearer header when token in localStorage', async () => {
    localStorage.setItem('nexspence_token', 'my-jwt')
    let capturedAuth = ''
    server.use(
      http.get('/api/v1/test-auth', ({ request }) => {
        capturedAuth = request.headers.get('Authorization') ?? ''
        return HttpResponse.json({ ok: true })
      })
    )
    await apiClient.get('/api/v1/test-auth')
    expect(capturedAuth).toBe('Bearer my-jwt')
  })

  it('sends no Authorization header when no token in localStorage', async () => {
    let capturedAuth: string | null = 'present'
    server.use(
      http.get('/api/v1/noauth-test', ({ request }) => {
        capturedAuth = request.headers.get('Authorization')
        return HttpResponse.json({ ok: true })
      })
    )
    await apiClient.get('/api/v1/noauth-test')
    expect(capturedAuth).toBeNull()
  })

  it('strips Content-Type header for FormData requests', async () => {
    let capturedType: string | null = 'application/json'
    server.use(
      http.post('/api/v1/upload-test', ({ request }) => {
        capturedType = request.headers.get('Content-Type')
        return HttpResponse.json({ ok: true })
      })
    )
    const form = new FormData()
    form.append('file', new Blob(['x']), 'test.txt')
    await apiClient.post('/api/v1/upload-test', form)
    // Content-Type must NOT be application/json after the interceptor strips it;
    // the browser/node fills in multipart/form-data with boundary
    expect(capturedType).not.toBe('application/json')
  })

  it('sets Content-Type application/json for regular JSON requests', async () => {
    let capturedType: string | null = null
    server.use(
      http.post('/api/v1/json-test', ({ request }) => {
        capturedType = request.headers.get('Content-Type')
        return HttpResponse.json({ ok: true })
      })
    )
    await apiClient.post('/api/v1/json-test', { key: 'value' })
    expect(capturedType).toContain('application/json')
  })
})

describe('apiClient response interceptor', () => {
  it('redirects to /login when a presented token is refused with 401', async () => {
    localStorage.setItem('nexspence_token', 'expired-token')
    server.use(
      http.get('/api/v1/protected', () =>
        HttpResponse.json({ error: 'unauthorized' }, { status: 401 })
      )
    )
    try {
      await apiClient.get('/api/v1/protected')
    } catch {
      // expected rejection
    }
    // The interceptor sets window.location.href = '/login'
    expect(window.location.href).toBe('/login')
  })

  // A signed-out visitor browsing public repositories (#404) meets 401s from
  // the signed-in-only endpoints; none of them is a session to lose.
  it('leaves a signed-out visitor where they are on 401 (no token presented)', async () => {
    let authHeader: string | null = 'unset'
    server.use(
      http.get('/api/v1/protected-anon', ({ request }) => {
        authHeader = request.headers.get('Authorization')
        return HttpResponse.json({ error: 'unauthorized' }, { status: 401 })
      })
    )
    await expect(apiClient.get('/api/v1/protected-anon')).rejects.toBeDefined()
    expect(authHeader).toBeNull()
    expect(window.location.href).toBe('http://localhost/')
  })

  it('clears nexspence_token from localStorage on 401 from non-login endpoint', async () => {
    localStorage.setItem('nexspence_token', 'stale-token')
    server.use(
      http.get('/api/v1/protected-clear', () =>
        HttpResponse.json({ error: 'unauthorized' }, { status: 401 })
      )
    )
    try {
      await apiClient.get('/api/v1/protected-clear')
    } catch {
      // expected rejection
    }
    expect(localStorage.getItem('nexspence_token')).toBeNull()
  })

  it('does NOT redirect to /login on 401 from the login endpoint', async () => {
    // Reset href so we can verify it did NOT change
    window.location.href = 'http://localhost/'
    server.use(
      http.post('/api/v1/login', () =>
        HttpResponse.json({ error: 'bad creds' }, { status: 401 })
      )
    )
    await expect(apiClient.post('/api/v1/login', {})).rejects.toBeDefined()
    // href should NOT have been set to '/login' — it stays at the base URL
    expect(window.location.href).toBe('http://localhost/')
  })

  it('still rejects the promise on 401 (never swallows the error)', async () => {
    server.use(
      http.get('/api/v1/always-401', () =>
        HttpResponse.json({ error: 'no access' }, { status: 401 })
      )
    )
    await expect(apiClient.get('/api/v1/always-401')).rejects.toBeDefined()
  })

  it('passes through successful 2xx responses unchanged', async () => {
    server.use(
      http.get('/api/v1/ok', () =>
        HttpResponse.json({ result: 'success' })
      )
    )
    const res = await apiClient.get('/api/v1/ok')
    expect(res.status).toBe(200)
    expect(res.data.result).toBe('success')
  })
})

// ── nexusApi helper smoke tests ─────────────────────────────────────────────
// These tests verify that each helper calls the right endpoint.
// MSW default handlers cover the expected routes.

describe('nexusApi helpers', () => {
  it('login calls POST /api/v1/login', async () => {
    const res = await nexusApi.login('admin', 'pass')
    expect(res.status).toBe(200)
  })

  it('me calls GET /api/v1/me', async () => {
    const res = await nexusApi.me()
    expect(res.status).toBe(200)
  })

  it('getAuthConfig calls GET /api/v1/auth/config', async () => {
    const cfg = await nexusApi.getAuthConfig()
    expect(cfg).toHaveProperty('oidcEnabled')
  })

  it('listRepositories calls GET /service/rest/v1/repositories', async () => {
    const res = await nexusApi.listRepositories()
    expect(Array.isArray(res.data)).toBe(true)
  })

  it('listRepositories passes format/type params', async () => {
    let capturedParams = ''
    server.use(
      http.get('/service/rest/v1/repositories', ({ request }) => {
        capturedParams = new URL(request.url).search
        return HttpResponse.json([])
      })
    )
    await nexusApi.listRepositories({ format: 'maven2', type: 'hosted' })
    expect(capturedParams).toContain('format=maven2')
  })

  it('getRepository calls GET /service/rest/v1/repositories/:name', async () => {
    const res = await nexusApi.getRepository('maven-hosted')
    expect(res.status).toBe(200)
  })

  it('createRepository calls POST /service/rest/v1/repositories/:format/:type', async () => {
    const res = await nexusApi.createRepository('maven2', 'hosted', { name: 'test' })
    expect(res.status).toBe(201)
  })

  it('deleteRepository calls DELETE /service/rest/v1/repositories/:name', async () => {
    const res = await nexusApi.deleteRepository('maven-hosted')
    expect(res.status).toBe(204)
  })

  it('updateRepository calls PUT /service/rest/v1/repositories/:format/:type/:name', async () => {
    const res = await nexusApi.updateRepository('maven2', 'hosted', 'maven-hosted', { online: true })
    expect(res.status).toBe(200)
  })

  it('patchRepository calls PATCH /service/rest/v1/repositories/:name', async () => {
    const res = await nexusApi.patchRepository('maven-hosted', { online: false })
    expect(res.status).toBe(200)
  })

  it('listComponents calls GET /service/rest/v1/components', async () => {
    const res = await nexusApi.listComponents('maven-hosted')
    expect(res.status).toBe(200)
  })

  it('listComponents passes continuationToken when provided', async () => {
    let capturedParams = ''
    server.use(
      http.get('/service/rest/v1/components', ({ request }) => {
        capturedParams = new URL(request.url).search
        return HttpResponse.json({ items: [], continuationToken: null })
      })
    )
    await nexusApi.listComponents('maven-hosted', 'abc123')
    expect(capturedParams).toContain('continuationToken=abc123')
  })

  it('search calls GET /service/rest/v1/search', async () => {
    const res = await nexusApi.search({ q: 'commons' })
    expect(res.status).toBe(200)
  })

  it('listUsers calls GET /service/rest/v1/security/users', async () => {
    const res = await nexusApi.listUsers()
    expect(Array.isArray(res.data)).toBe(true)
  })

  it('createUser calls POST /service/rest/v1/security/users', async () => {
    const res = await nexusApi.createUser({ username: 'bob' })
    expect(res.status).toBe(201)
  })

  it('updateUser calls PUT /service/rest/v1/security/users/:userId', async () => {
    const res = await nexusApi.updateUser('user-1', { email: 'new@test.com' })
    expect(res.status).toBe(200)
  })

  it('deleteUser calls DELETE /service/rest/v1/security/users/:userId', async () => {
    server.use(
      http.delete('/service/rest/v1/security/users/:userId', () =>
        new HttpResponse(null, { status: 204 })
      )
    )
    const res = await nexusApi.deleteUser('user-1')
    expect(res.status).toBe(204)
  })

  it('changePassword calls PUT /service/rest/v1/security/users/:userId/change-password', async () => {
    server.use(
      http.put('/service/rest/v1/security/users/:userId/change-password', () =>
        new HttpResponse(null, { status: 200 })
      )
    )
    const res = await nexusApi.changePassword('user-1', 'newpass')
    expect(res.status).toBe(200)
  })

  it('listRoles calls GET /service/rest/v1/security/roles', async () => {
    const res = await nexusApi.listRoles()
    expect(Array.isArray(res.data)).toBe(true)
  })

  it('createRole calls POST /service/rest/v1/security/roles', async () => {
    const res = await nexusApi.createRole({ name: 'dev', description: 'Developer' })
    expect(res.status).toBe(201)
  })

  it('deleteRole calls DELETE /service/rest/v1/security/roles/:id', async () => {
    const res = await nexusApi.deleteRole('role-1')
    expect(res.status).toBe(204)
  })

  it('setUserRoles calls PUT /service/rest/v1/security/users/:userId/roles', async () => {
    const res = await nexusApi.setUserRoles('user-1', ['role-1'])
    expect(res.status).toBe(204)
  })

  it('listRolePrivileges calls GET /service/rest/v1/security/roles/:roleId/privileges', async () => {
    const res = await nexusApi.listRolePrivileges('role-1')
    expect(res.status).toBe(200)
  })

  it('setRolePrivileges calls PUT /service/rest/v1/security/roles/:roleId/privileges', async () => {
    const res = await nexusApi.setRolePrivileges('role-1', ['priv-1'])
    expect(res.status).toBe(204)
  })

  it('updateRole calls PUT /service/rest/v1/security/roles/:id', async () => {
    server.use(
      http.put('/service/rest/v1/security/roles/:id', () =>
        HttpResponse.json({ id: 'role-1', name: 'updated' })
      )
    )
    const res = await nexusApi.updateRole('role-1', { name: 'updated' })
    expect(res.status).toBe(200)
  })

  it('listPrivileges calls GET /service/rest/v1/security/privileges', async () => {
    const res = await nexusApi.listPrivileges()
    expect(Array.isArray(res.data)).toBe(true)
  })

  it('createPrivilege calls POST /service/rest/v1/security/privileges', async () => {
    const res = await nexusApi.createPrivilege({ type: 'repository-content-selector' })
    expect(res.status).toBe(201)
  })

  it('updatePrivilege calls PUT /service/rest/v1/security/privileges/:id', async () => {
    server.use(
      http.put('/service/rest/v1/security/privileges/:id', () =>
        HttpResponse.json({ id: 'priv-1' })
      )
    )
    const res = await nexusApi.updatePrivilege('priv-1', { type: 'repo' })
    expect(res.status).toBe(200)
  })

  it('deletePrivilege calls DELETE /service/rest/v1/security/privileges/:id', async () => {
    const res = await nexusApi.deletePrivilege('priv-1')
    expect(res.status).toBe(204)
  })

  it('listContentSelectors calls GET /service/rest/v1/security/content-selectors', async () => {
    const res = await nexusApi.listContentSelectors()
    expect(Array.isArray(res.data)).toBe(true)
  })

  it('createContentSelector calls POST /service/rest/v1/security/content-selectors', async () => {
    const res = await nexusApi.createContentSelector({ name: 'cs', expression: 'path =~ "/*"' })
    expect(res.status).toBe(201)
  })

  it('updateContentSelector calls PUT /service/rest/v1/security/content-selectors/:id', async () => {
    server.use(
      http.put('/service/rest/v1/security/content-selectors/:id', () =>
        HttpResponse.json({ id: 'cs-1' })
      )
    )
    const res = await nexusApi.updateContentSelector('cs-1', { name: 'cs' })
    expect(res.status).toBe(200)
  })

  it('deleteContentSelector calls DELETE /service/rest/v1/security/content-selectors/:id', async () => {
    const res = await nexusApi.deleteContentSelector('cs-1')
    expect(res.status).toBe(204)
  })

  it('attachContentSelector calls PUT /service/rest/v1/security/privileges/:name/content-selector/:id', async () => {
    server.use(
      http.put('/service/rest/v1/security/privileges/:privilegeName/content-selector/:selectorId', () =>
        new HttpResponse(null, { status: 204 })
      )
    )
    const res = await nexusApi.attachContentSelector('priv-1', 'cs-1')
    expect(res.status).toBe(204)
  })

  it('detachContentSelector calls DELETE /service/rest/v1/security/privileges/:name/content-selector', async () => {
    server.use(
      http.delete('/service/rest/v1/security/privileges/:privilegeName/content-selector', () =>
        new HttpResponse(null, { status: 204 })
      )
    )
    const res = await nexusApi.detachContentSelector('priv-1')
    expect(res.status).toBe(204)
  })

  it('listBlobStores calls GET /service/rest/v1/blobstores', async () => {
    const res = await nexusApi.listBlobStores()
    expect(Array.isArray(res.data)).toBe(true)
  })

  it('createBlobStore calls POST /service/rest/v1/blobstores/:type', async () => {
    const res = await nexusApi.createBlobStore('file', { name: 'local' })
    expect(res.status).toBe(201)
  })

  it('updateBlobStore calls PUT /service/rest/v1/blobstores/:type/:name', async () => {
    server.use(
      http.put('/service/rest/v1/blobstores/:type/:name', () =>
        HttpResponse.json({ name: 'local' })
      )
    )
    const res = await nexusApi.updateBlobStore('file', 'local', { name: 'local' })
    expect(res.status).toBe(200)
  })

  it('deleteBlobStore calls DELETE /service/rest/v1/blobstores/:name', async () => {
    const res = await nexusApi.deleteBlobStore('local')
    expect(res.status).toBe(204)
  })

  it('getBlobStoreUsage calls GET /api/v1/blob-stores/:name/usage', async () => {
    const res = await nexusApi.getBlobStoreUsage('local')
    expect(res.status).toBe(200)
  })

  it('testBlobStore calls POST /api/v1/blobstores/test', async () => {
    const res = await nexusApi.testBlobStore('file', { path: '/tmp' })
    expect(res.status).toBe(200)
  })

  it('listCleanupPolicies calls GET /service/rest/v1/cleanup-policies', async () => {
    const res = await nexusApi.listCleanupPolicies()
    expect(Array.isArray(res.data)).toBe(true)
  })

  it('createCleanupPolicy calls POST /service/rest/v1/cleanup-policies', async () => {
    const res = await nexusApi.createCleanupPolicy({ name: 'old' })
    expect(res.status).toBe(201)
  })

  it('deleteCleanupPolicy calls DELETE /service/rest/v1/cleanup-policies/:id', async () => {
    const res = await nexusApi.deleteCleanupPolicy('cp-1')
    expect(res.status).toBe(204)
  })

  it('getCleanupPolicy calls GET /service/rest/v1/cleanup-policies/:id', async () => {
    server.use(
      http.get('/service/rest/v1/cleanup-policies/:id', () =>
        HttpResponse.json({ id: 'cp-1', name: 'old' })
      )
    )
    const res = await nexusApi.getCleanupPolicy('cp-1')
    expect(res.status).toBe(200)
  })

  it('updateCleanupPolicy calls PUT /service/rest/v1/cleanup-policies/:id', async () => {
    server.use(
      http.put('/service/rest/v1/cleanup-policies/:id', () =>
        HttpResponse.json({ id: 'cp-1' })
      )
    )
    const res = await nexusApi.updateCleanupPolicy('cp-1', { name: 'new' })
    expect(res.status).toBe(200)
  })

  it('runCleanupPolicy calls POST /service/rest/v1/cleanup-policies/:id/run', async () => {
    server.use(
      http.post('/service/rest/v1/cleanup-policies/:id/run', () =>
        new HttpResponse(null, { status: 204 })
      )
    )
    const res = await nexusApi.runCleanupPolicy('cp-1')
    expect(res.status).toBe(204)
  })

  it('previewCleanupPolicy calls POST /api/v1/cleanup-policies/:id/preview', async () => {
    server.use(
      http.post('/api/v1/cleanup-policies/:id/preview', () =>
        HttpResponse.json({ assets: [], totalCount: 0, totalBytes: 0 })
      )
    )
    const res = await nexusApi.previewCleanupPolicy('cp-1')
    expect(res.status).toBe(200)
  })

  it('listRoutingRules calls GET /service/rest/v1/routing-rules', async () => {
    const res = await nexusApi.listRoutingRules()
    expect(Array.isArray(res.data)).toBe(true)
  })

  it('createRoutingRule calls POST /service/rest/v1/routing-rules', async () => {
    server.use(
      http.post('/service/rest/v1/routing-rules', () =>
        HttpResponse.json({ id: 'rr-1' }, { status: 201 })
      )
    )
    const res = await nexusApi.createRoutingRule({ name: 'rr', mode: 'ALLOW', matchers: ['.*/maven.*'] })
    expect(res.status).toBe(201)
  })

  it('updateRoutingRule calls PUT /service/rest/v1/routing-rules/:id', async () => {
    server.use(
      http.put('/service/rest/v1/routing-rules/:id', () =>
        HttpResponse.json({ id: 'rr-1' })
      )
    )
    const res = await nexusApi.updateRoutingRule('rr-1', { name: 'rr', mode: 'BLOCK', matchers: [] })
    expect(res.status).toBe(200)
  })

  it('deleteRoutingRule calls DELETE /service/rest/v1/routing-rules/:id', async () => {
    server.use(
      http.delete('/service/rest/v1/routing-rules/:id', () =>
        new HttpResponse(null, { status: 204 })
      )
    )
    const res = await nexusApi.deleteRoutingRule('rr-1')
    expect(res.status).toBe(204)
  })

  it('listAuditEvents calls GET /service/rest/v1/audit', async () => {
    const res = await nexusApi.listAuditEvents()
    expect(res.status).toBe(200)
  })

  it('listAuditEvents passes filter params', async () => {
    let capturedSearch = ''
    server.use(
      http.get('/service/rest/v1/audit', ({ request }) => {
        capturedSearch = new URL(request.url).search
        return HttpResponse.json({ items: [], total: 0 })
      })
    )
    await nexusApi.listAuditEvents({ username: 'admin', domain: 'REPO' })
    expect(capturedSearch).toContain('username=admin')
  })

  it('auditExportUrl returns correct URL with format=ndjson', () => {
    const url = nexusApi.auditExportUrl()
    expect(url).toContain('/service/rest/v1/audit')
    expect(url).toContain('format=ndjson')
  })

  it('auditExportUrl includes optional filter params when provided', () => {
    const url = nexusApi.auditExportUrl({ username: 'admin', domain: 'REPO', action: 'CREATE', from: '2026-01-01', to: '2026-06-01' })
    expect(url).toContain('username=admin')
    expect(url).toContain('domain=REPO')
    expect(url).toContain('action=CREATE')
    expect(url).toContain('from=2026-01-01')
    expect(url).toContain('to=2026-06-01')
  })

  it('getMetrics calls GET /api/v1/metrics', async () => {
    const res = await nexusApi.getMetrics()
    expect(res.status).toBe(200)
  })

  it('getStatus calls GET /service/rest/v1/status', async () => {
    const res = await nexusApi.getStatus()
    expect(res.status).toBe(200)
  })

  it('listPathTree calls GET /api/v1/browse/repositories/:name/path-tree without q', async () => {
    const res = await nexusApi.listPathTree('my-repo')
    expect(res.status).toBe(200)
  })

  it('listPathTree passes q param when provided', async () => {
    let capturedSearch = ''
    server.use(
      http.get('/api/v1/browse/repositories/:name/path-tree', ({ request }) => {
        capturedSearch = new URL(request.url).search
        return HttpResponse.json({ paths: [] })
      })
    )
    await nexusApi.listPathTree('my-repo', 'foo')
    expect(capturedSearch).toContain('q=foo')
  })

  it('getComponent calls GET /service/rest/v1/components/:id', async () => {
    server.use(
      http.get('/service/rest/v1/components/:id', () =>
        HttpResponse.json({ id: 'comp-1' })
      )
    )
    const res = await nexusApi.getComponent('comp-1')
    expect(res.status).toBe(200)
  })

  it('deleteComponent calls DELETE /service/rest/v1/components/:id', async () => {
    server.use(
      http.delete('/service/rest/v1/components/:id', () =>
        new HttpResponse(null, { status: 204 })
      )
    )
    const res = await nexusApi.deleteComponent('comp-1')
    expect(res.status).toBe(204)
  })

  it('setComponentTags calls PUT /service/rest/v1/components/:id/tags', async () => {
    server.use(
      http.put('/service/rest/v1/components/:id/tags', () =>
        HttpResponse.json({ tags: ['v1'] })
      )
    )
    const res = await nexusApi.setComponentTags('comp-1', ['v1'])
    expect(res.status).toBe(200)
  })

  it('getUser calls GET /service/rest/v1/security/users/:userId', async () => {
    server.use(
      http.get('/service/rest/v1/security/users/:userId', () =>
        HttpResponse.json({ id: 'user-1', username: 'admin' })
      )
    )
    const res = await nexusApi.getUser('user-1')
    expect(res.status).toBe(200)
  })
})

// ── nexspenceApi helper smoke tests ─────────────────────────────────────────

describe('nexspenceApi helpers', () => {
  it('listMigrationJobs calls GET /api/v1/migration/jobs', async () => {
    const res = await nexspenceApi.listMigrationJobs()
    expect(Array.isArray(res.data)).toBe(true)
  })

  it('createMigrationJob calls POST /api/v1/migration/jobs', async () => {
    const res = await nexspenceApi.createMigrationJob({ sourceUrl: 'http://nexus' })
    expect(res.status).toBe(201)
  })

  it('getMigrationJob calls GET /api/v1/migration/jobs/:id', async () => {
    server.use(
      http.get('/api/v1/migration/jobs/:id', () =>
        HttpResponse.json({ id: 'job-1', status: 'running' })
      )
    )
    const res = await nexspenceApi.getMigrationJob('job-1')
    expect(res.status).toBe(200)
  })

  it('pauseMigrationJob calls POST /api/v1/migration/jobs/:id/pause', async () => {
    server.use(
      http.post('/api/v1/migration/jobs/:id/pause', () =>
        new HttpResponse(null, { status: 204 })
      )
    )
    const res = await nexspenceApi.pauseMigrationJob('job-1')
    expect(res.status).toBe(204)
  })

  it('resumeMigrationJob calls POST /api/v1/migration/jobs/:id/resume', async () => {
    server.use(
      http.post('/api/v1/migration/jobs/:id/resume', () =>
        new HttpResponse(null, { status: 204 })
      )
    )
    const res = await nexspenceApi.resumeMigrationJob('job-1')
    expect(res.status).toBe(204)
  })

  it('previewMigration calls POST /api/v1/migration/preview', async () => {
    server.use(
      http.post('/api/v1/migration/preview', () =>
        HttpResponse.json({ repos: [] })
      )
    )
    const res = await nexspenceApi.previewMigration({ sourceUrl: 'http://nexus' })
    expect(res.status).toBe(200)
  })

  it('getSystemInfo calls GET /api/v1/system/info', async () => {
    const res = await nexspenceApi.getSystemInfo()
    expect(res.status).toBe(200)
  })

  it('getServiceStatuses calls GET /api/v1/system/services', async () => {
    const res = await nexspenceApi.getServiceStatuses()
    expect(Array.isArray(res.data)).toBe(true)
  })

  it('exportBackup calls GET /api/v1/backup/export', async () => {
    server.use(
      http.get('/api/v1/backup/export', () =>
        new HttpResponse(new Blob(['data']), { status: 200 })
      )
    )
    const res = await nexspenceApi.exportBackup()
    expect(res.status).toBe(200)
  })

  it('restoreBackup calls POST /api/v1/backup/restore with FormData', async () => {
    server.use(
      http.post('/api/v1/backup/restore', () =>
        HttpResponse.json({ restored: { repos: 5 } })
      )
    )
    const file = new File(['content'], 'backup.tar.gz')
    const res = await nexspenceApi.restoreBackup(file)
    expect(res.status).toBe(200)
  })

  it('exportRepo calls GET /api/v1/repositories/:name/export', async () => {
    server.use(
      http.get('/api/v1/repositories/:name/export', () =>
        new HttpResponse(new Blob(['data']), { status: 200 })
      )
    )
    const res = await nexspenceApi.exportRepo('maven-hosted')
    expect(res.status).toBe(200)
  })

  it('importRepo calls POST /api/v1/repositories/import with FormData', async () => {
    server.use(
      http.post('/api/v1/repositories/import', () =>
        HttpResponse.json({ imported: { repository: 'maven-hosted', components: 1, assets: 1, blobs: 1, conflictMode: 'skip' } })
      )
    )
    const file = new File(['content'], 'repo.tar.gz')
    const res = await nexspenceApi.importRepo(file, 'maven-hosted', 'skip')
    expect(res.status).toBe(200)
  })

  it('getRepositoryQuota calls GET /api/v1/repositories/:name/quota', async () => {
    const res = await nexspenceApi.getRepositoryQuota('maven-hosted')
    expect(res.status).toBe(200)
  })

  it('getDockerBrowseTree calls GET /api/v1/browse/repositories/:name/docker-tree', async () => {
    const res = await nexspenceApi.getDockerBrowseTree('docker-hosted')
    expect(res.status).toBe(200)
  })

  it('getRawBrowseTree calls GET /api/v1/browse/repositories/:name/raw-tree', async () => {
    const res = await nexspenceApi.getRawBrowseTree('raw-hosted')
    expect(res.status).toBe(200)
  })

  it('privilegeRoleMap calls GET /api/v1/security/privilege-role-map', async () => {
    const data = await nexspenceApi.privilegeRoleMap()
    expect(typeof data).toBe('object')
  })

  it('myPrivileges calls GET /api/v1/me/privileges', async () => {
    const data = await nexspenceApi.myPrivileges()
    expect(Array.isArray(data)).toBe(true)
  })

  it('deleteByPath calls DELETE /api/v1/browse/repositories/:name/path', async () => {
    server.use(
      http.delete('/api/v1/browse/repositories/:name/path', () =>
        new HttpResponse(null, { status: 204 })
      )
    )
    const res = await nexspenceApi.deleteByPath('raw-hosted', '/foo/bar.txt')
    expect(res.status).toBe(204)
  })

  it('deleteDockerTag calls DELETE /api/v1/browse/repositories/:name/docker-tag', async () => {
    server.use(
      http.delete('/api/v1/browse/repositories/:name/docker-tag', () =>
        new HttpResponse(null, { status: 204 })
      )
    )
    const res = await nexspenceApi.deleteDockerTag('docker-hosted', 'nginx', 'latest')
    expect(res.status).toBe(204)
  })

  it('deleteDockerImage calls DELETE /api/v1/browse/repositories/:name/docker-image', async () => {
    server.use(
      http.delete('/api/v1/browse/repositories/:name/docker-image', () =>
        new HttpResponse(null, { status: 204 })
      )
    )
    const res = await nexspenceApi.deleteDockerImage('docker-hosted', 'nginx')
    expect(res.status).toBe(204)
  })

  it('getScanResult returns data:null for 204', async () => {
    const res = await nexspenceApi.getScanResult('comp-1')
    expect(res.data).toBeNull()
  })

  it('getScanResult returns data when scan has results', async () => {
    server.use(
      http.get('/api/v1/components/:id/scan', () =>
        HttpResponse.json({ vulnerabilities: [] })
      )
    )
    const res = await nexspenceApi.getScanResult('comp-1')
    expect(res.data).toHaveProperty('vulnerabilities')
  })

  it('scanComponent calls POST /api/v1/components/:id/scan', async () => {
    server.use(
      http.post('/api/v1/components/:id/scan', () =>
        HttpResponse.json({ status: 'scanning' })
      )
    )
    const res = await nexspenceApi.scanComponent('comp-1')
    expect(res.status).toBe(200)
  })

  it('scanComponent passes imageRef body when provided', async () => {
    let capturedBody: unknown = null
    server.use(
      http.post('/api/v1/components/:id/scan', async ({ request }) => {
        capturedBody = await request.json()
        return HttpResponse.json({ status: 'scanning' })
      })
    )
    await nexspenceApi.scanComponent('comp-1', { imageRef: 'nginx:latest' })
    expect((capturedBody as Record<string, unknown>)?.imageRef).toBe('nginx:latest')
  })

  it('listReplicationRules calls GET /api/v1/replication/rules', async () => {
    const res = await nexspenceApi.listReplicationRules()
    expect(Array.isArray(res.data)).toBe(true)
  })

  it('createReplicationRule calls POST /api/v1/replication/rules', async () => {
    server.use(
      http.post('/api/v1/replication/rules', () =>
        HttpResponse.json({ id: 'rr-1' }, { status: 201 })
      )
    )
    const res = await nexspenceApi.createReplicationRule({
      name: 'sync', source_repo: 'maven-hosted', target_url: 'http://remote',
      target_repo: 'maven-remote', target_username: 'admin', target_password: 'pass',
      cron_expr: '0 * * * *', enabled: true,
    })
    expect(res.status).toBe(201)
  })

  it('updateReplicationRule calls PUT /api/v1/replication/rules/:id', async () => {
    server.use(
      http.put('/api/v1/replication/rules/:id', () =>
        HttpResponse.json({ id: 'rr-1' })
      )
    )
    const res = await nexspenceApi.updateReplicationRule('rr-1', {
      name: 'sync', source_repo: 'maven-hosted', target_url: 'http://remote',
      target_repo: 'maven-remote', target_username: 'admin', target_password: 'pass',
      cron_expr: '0 * * * *', enabled: true,
    })
    expect(res.status).toBe(200)
  })

  it('deleteReplicationRule calls DELETE /api/v1/replication/rules/:id', async () => {
    server.use(
      http.delete('/api/v1/replication/rules/:id', () =>
        new HttpResponse(null, { status: 204 })
      )
    )
    const res = await nexspenceApi.deleteReplicationRule('rr-1')
    expect(res.status).toBe(204)
  })

  it('runReplicationRule calls POST /api/v1/replication/rules/:id/run', async () => {
    server.use(
      http.post('/api/v1/replication/rules/:id/run', () =>
        new HttpResponse(null, { status: 204 })
      )
    )
    const res = await nexspenceApi.runReplicationRule('rr-1')
    expect(res.status).toBe(204)
  })

  it('testReplicationRule calls POST /api/v1/replication/rules/:id/test', async () => {
    server.use(
      http.post('/api/v1/replication/rules/:id/test', () =>
        HttpResponse.json({ ok: true })
      )
    )
    const res = await nexspenceApi.testReplicationRule('rr-1')
    expect(res.status).toBe(200)
  })

  it('listReplicationHistory calls GET /api/v1/replication/rules/:id/history', async () => {
    server.use(
      http.get('/api/v1/replication/rules/:id/history', () =>
        HttpResponse.json([])
      )
    )
    const res = await nexspenceApi.listReplicationHistory('rr-1')
    expect(Array.isArray(res.data)).toBe(true)
  })
})

// ── Blob store migration standalone functions ───────────────────────────────

describe('startBlobStoreMigration', () => {
  it('POSTs to /api/v1/repositories/:name/migrate-blob-store and returns migration', async () => {
    const migration = {
      id: 'mig-1', repositoryName: 'maven-hosted', sourceStoreId: 'store-a',
      targetStoreId: 'store-b', status: 'running' as const,
      totalAssets: 10, doneAssets: 0, totalBytes: 1000, doneBytes: 0,
      errorMessage: null, startedAt: null, finishedAt: null,
      createdAt: '2026-06-01T00:00:00Z', updatedAt: '2026-06-01T00:00:00Z',
    }
    server.use(
      http.post('/api/v1/repositories/:name/migrate-blob-store', () =>
        HttpResponse.json(migration, { status: 202 })
      )
    )
    const result = await startBlobStoreMigration('maven-hosted', 'store-b')
    expect(result.id).toBe('mig-1')
    expect(result.status).toBe('running')
  })
})

describe('getBlobStoreMigration', () => {
  it('returns migration data when found', async () => {
    const migration = {
      id: 'mig-1', repositoryName: 'maven-hosted', sourceStoreId: 'store-a',
      targetStoreId: 'store-b', status: 'done' as const,
      totalAssets: 10, doneAssets: 10, totalBytes: 1000, doneBytes: 1000,
      errorMessage: null, startedAt: '2026-06-01T00:00:00Z', finishedAt: '2026-06-01T01:00:00Z',
      createdAt: '2026-06-01T00:00:00Z', updatedAt: '2026-06-01T01:00:00Z',
    }
    server.use(
      http.get('/api/v1/repositories/:name/blob-store-migration', () =>
        HttpResponse.json(migration)
      )
    )
    const result = await getBlobStoreMigration('maven-hosted')
    expect(result?.id).toBe('mig-1')
  })

  it('returns null when 404 (no migration in progress)', async () => {
    // Default handler returns 404 — rely on that
    const result = await getBlobStoreMigration('maven-hosted')
    expect(result).toBeNull()
  })

  it('throws for non-404 errors', async () => {
    server.use(
      http.get('/api/v1/repositories/:name/blob-store-migration', () =>
        HttpResponse.json({ error: 'server error' }, { status: 500 })
      )
    )
    await expect(getBlobStoreMigration('maven-hosted')).rejects.toBeDefined()
  })
})

describe('cancelBlobStoreMigration', () => {
  it('DELETEs /api/v1/repositories/:name/blob-store-migration', async () => {
    server.use(
      http.delete('/api/v1/repositories/:name/blob-store-migration', () =>
        new HttpResponse(null, { status: 204 })
      )
    )
    await expect(cancelBlobStoreMigration('maven-hosted')).resolves.toBeUndefined()
  })
})
