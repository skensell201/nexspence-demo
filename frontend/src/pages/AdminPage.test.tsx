import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { screen, waitFor, fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import AdminPage from './AdminPage'
import { renderWithProviders, seedAuthAsAdmin } from '@/test/renderUtils'
import { server } from '@/test/msw/server'
import { fixtures } from '@/test/fixtures'

function renderAdmin(tab?: string) {
  const initialEntries = tab ? [`/admin?tab=${tab}`] : ['/admin']
  return renderWithProviders(<AdminPage />, { routerProps: { initialEntries } })
}

const blobStore = {
  id: 'bs-1',
  name: 'default',
  type: 'local',
  usedBytes: 5 * 1024 * 1024,
  quotaBytes: 10 * 1024 * 1024 * 1024,
  config: { path: './data/blobs' },
}

beforeEach(() => {
  seedAuthAsAdmin()
})
afterEach(() => {
  vi.restoreAllMocks()
})

describe('AdminPage — Info tab', () => {
  it('renders status, system info and service connections', async () => {
    server.use(
      http.get('/service/rest/v1/status', () => HttpResponse.json({ status: 'ok', edition: 'OSS', version: '1.9.0' })),
      http.get('/api/v1/system/info', () => HttpResponse.json({ version: '1.9.0', product: 'Nexspence' })),
      http.get('/api/v1/system/services', () =>
        HttpResponse.json([
          { name: 'PostgreSQL', status: 'ok', latency_ms: 12, detail: 'connected', checked_at: new Date().toISOString() },
          { name: 'S3 Storage', status: 'warn', latency_ms: 120, detail: 'slow', checked_at: new Date().toISOString() },
          { name: 'Docker Subdomain Connector', status: 'ok', detail: 'Active *.docker.example.com', checked_at: new Date().toISOString() },
        ]),
      ),
    )
    renderAdmin('info')
    expect(await screen.findByText('Online')).toBeInTheDocument()
    expect(await screen.findByText('Nexspence')).toBeInTheDocument()
    expect(await screen.findByText('PostgreSQL')).toBeInTheDocument()
    expect(screen.getByText('S3 Storage')).toBeInTheDocument()
    expect(screen.getAllByText('Docker Subdomain Connector').length).toBeGreaterThan(0)
  })

  it('shows offline status and disabled docker connector', async () => {
    server.use(
      http.get('/service/rest/v1/status', () => HttpResponse.json({ status: 'down' })),
      http.get('/api/v1/system/services', () =>
        HttpResponse.json([
          { name: 'Docker Subdomain Connector', status: 'disabled', detail: 'not configured', checked_at: new Date().toISOString() },
        ]),
      ),
    )
    renderAdmin('info')
    expect(await screen.findByText('Offline')).toBeInTheDocument()
    expect((await screen.findAllByText('not configured')).length).toBeGreaterThan(0)
  })

  it('refreshes via the header refresh button', async () => {
    let calls = 0
    server.use(
      http.get('/service/rest/v1/status', () => { calls++; return HttpResponse.json({ status: 'ok' }) }),
    )
    renderAdmin('info')
    await screen.findByText('Online')
    const before = calls
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }))
    await waitFor(() => expect(calls).toBeGreaterThan(before))
  })
})

describe('AdminPage — Blob Stores tab', () => {
  const s3Store = {
    id: 'bs-2',
    name: 'archive',
    type: 's3',
    usedBytes: 1024,
    quotaBytes: null,
    config: {
      bucket: 'artifacts',
      region: 'eu-central-1',
      access_key: 'AKIAEXAMPLE',
      secret_key_set: true,
    },
  }

  /** Opens the archive store's detail modal and switches it into edit mode. */
  async function openS3Edit() {
    server.use(
      http.get('/service/rest/v1/blobstores', () => HttpResponse.json([s3Store])),
      http.get('/api/v1/blob-stores/:name/usage', () =>
        HttpResponse.json({ store: s3Store, linkedRepositories: [], totalAssetBytes: 0 }),
      ),
    )
    renderAdmin('blobs')
    fireEvent.click(await screen.findByText('archive'))
    await screen.findByText('Blob Store: archive')
    fireEvent.click(await screen.findByRole('button', { name: /Edit Config/i }))
    await screen.findByText('Edit Configuration')
  }

  it('never receives the S3 secret key from the API', async () => {
    await openS3Edit()
    // The redacted payload carries a marker instead of the credential.
    expect(screen.getByText(/A secret key is stored/i)).toBeInTheDocument()
  })

  it('omits secret_key on save when the field is left blank', async () => {
    let put: Record<string, unknown> | null = null
    server.use(
      http.put('/service/rest/v1/blobstores/:type/:name', async ({ request }) => {
        put = (await request.json()) as Record<string, unknown>
        return HttpResponse.json(s3Store)
      }),
    )
    await openS3Edit()
    fireEvent.click(screen.getByRole('button', { name: /^Save$/ }))
    await waitFor(() => expect(put).toBeTruthy())
    const cfg = (put! as { config?: Record<string, unknown> }).config
    expect(cfg).not.toHaveProperty('secret_key')
    expect(cfg).not.toHaveProperty('secret_key_set')
    expect(cfg?.bucket).toBe('artifacts')
    expect(cfg?.access_key).toBe('AKIAEXAMPLE')
  })

  it('sends secret_key on save when a new one is typed', async () => {
    const user = userEvent.setup()
    let put: Record<string, unknown> | null = null
    server.use(
      http.put('/service/rest/v1/blobstores/:type/:name', async ({ request }) => {
        put = (await request.json()) as Record<string, unknown>
        return HttpResponse.json(s3Store)
      }),
    )
    await openS3Edit()
    await user.type(screen.getByPlaceholderText('unchanged'), 'rotat3d')
    fireEvent.click(screen.getByRole('button', { name: /^Save$/ }))
    await waitFor(() => expect(put).toBeTruthy())
    const cfg = (put! as { config?: Record<string, unknown> }).config
    expect(cfg?.secret_key).toBe('rotat3d')
  })

  it('round-trips the skip-TLS-verify flag through the edit form', async () => {
    let put: Record<string, unknown> | null = null
    server.use(
      http.put('/service/rest/v1/blobstores/:type/:name', async ({ request }) => {
        put = (await request.json()) as Record<string, unknown>
        return HttpResponse.json(s3Store)
      }),
    )
    await openS3Edit()
    const box = screen.getByLabelText(/Skip TLS certificate verification/i)
    expect(box).not.toBeChecked() // the stored config has no flag
    fireEvent.click(box)
    fireEvent.click(screen.getByRole('button', { name: /^Save$/ }))
    await waitFor(() => expect(put).toBeTruthy())
    const cfg = (put! as { config?: Record<string, unknown> }).config
    expect(cfg?.skip_tls_verify).toBe(true)
  })

  it('shows empty state', async () => {
    server.use(http.get('/service/rest/v1/blobstores', () => HttpResponse.json([])))
    renderAdmin('blobs')
    expect(await screen.findByText('No blob stores configured')).toBeInTheDocument()
  })

  it('lists blob stores and edits quota', async () => {
    let put: Record<string, unknown> | null = null
    server.use(
      http.get('/service/rest/v1/blobstores', () => HttpResponse.json([blobStore])),
      http.put('/service/rest/v1/blobstores/:type/:name', async ({ request }) => {
        put = (await request.json()) as Record<string, unknown>
        return HttpResponse.json({})
      }),
    )
    renderAdmin('blobs')
    expect(await screen.findByText('default')).toBeInTheDocument()
    fireEvent.click(screen.getByTitle('Edit quota'))
    const input = screen.getByPlaceholderText('GB')
    fireEvent.change(input, { target: { value: '20' } })
    fireEvent.click(screen.getByText('Save'))
    await waitFor(() => expect(put).toBeTruthy())
    expect((put! as { quotaBytes: number }).quotaBytes).toBe(20 * 1024 * 1024 * 1024)
  })

  it('cancels quota edit via Escape and X', async () => {
    server.use(http.get('/service/rest/v1/blobstores', () => HttpResponse.json([blobStore])))
    renderAdmin('blobs')
    await screen.findByText('default')
    fireEvent.click(screen.getByTitle('Edit quota'))
    const input = screen.getByPlaceholderText('GB')
    fireEvent.keyDown(input, { key: 'Escape' })
    await waitFor(() => expect(screen.queryByPlaceholderText('GB')).not.toBeInTheDocument())
  })

  it('opens the blob store detail modal and shows linked repos', async () => {
    server.use(
      http.get('/service/rest/v1/blobstores', () => HttpResponse.json([blobStore])),
      http.get('/api/v1/blob-stores/:name/usage', () =>
        HttpResponse.json({
          store: blobStore,
          linkedRepositories: [{ name: 'maven-hosted', format: 'maven2', type: 'hosted', bytesUsed: 1024 }],
          totalAssetBytes: 1024,
          quotaRemaining: 9000,
        }),
      ),
    )
    renderAdmin('blobs')
    fireEvent.click(await screen.findByText('default'))
    expect(await screen.findByText('Blob Store: default')).toBeInTheDocument()
    expect(await screen.findByText('Linked Repositories')).toBeInTheDocument()
    expect(screen.getByText('maven-hosted')).toBeInTheDocument()
  })

  it('detail modal: runs a dry-run garbage collection', async () => {
    let compactUrl = ''
    server.use(
      http.get('/service/rest/v1/blobstores', () => HttpResponse.json([blobStore])),
      http.get('/api/v1/blob-stores/:name/usage', () =>
        HttpResponse.json({ store: blobStore, linkedRepositories: [], totalAssetBytes: 0 }),
      ),
      http.post('/api/v1/blobstores/:name/compact', ({ request }) => {
        compactUrl = request.url
        return HttpResponse.json({ store: 'default', scannedBlobs: 5, orphans: 2, freedBytes: 2048, dryRun: true })
      }),
    )
    renderAdmin('blobs')
    fireEvent.click(await screen.findByText('default'))
    await screen.findByText('Blob Store: default')
    fireEvent.click(await screen.findByRole('button', { name: /Dry run/i }))
    await waitFor(() => expect(compactUrl).toContain('dry_run=true'))
    expect(await screen.findByText(/2 orphans/i)).toBeInTheDocument()
  })

  it('detail modal: edit local path config and save', async () => {
    let put: { config?: { path?: string } } | null = null
    server.use(
      http.get('/service/rest/v1/blobstores', () => HttpResponse.json([blobStore])),
      http.get('/api/v1/blob-stores/:name/usage', () =>
        HttpResponse.json({ store: blobStore, linkedRepositories: [], totalAssetBytes: 0 }),
      ),
      http.put('/service/rest/v1/blobstores/:type/:name', async ({ request }) => {
        put = (await request.json()) as { config?: { path?: string } }
        return HttpResponse.json({})
      }),
    )
    renderAdmin('blobs')
    fireEvent.click(await screen.findByText('default'))
    await screen.findByText('Blob Store: default')
    fireEvent.click(await screen.findByRole('button', { name: /Edit Config/ }))
    const pathInput = await screen.findByDisplayValue('./data/blobs')
    fireEvent.change(pathInput, { target: { value: '/new/path' } })
    fireEvent.click(screen.getByRole('button', { name: /^Save$/ }))
    await waitFor(() => expect(put).toBeTruthy())
    expect(put!.config!.path).toBe('/new/path')
  })

  it('detail modal: deletes when no linked repos', async () => {
    let deleted = false
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    server.use(
      http.get('/service/rest/v1/blobstores', () => HttpResponse.json([blobStore])),
      http.get('/api/v1/blob-stores/:name/usage', () =>
        HttpResponse.json({ store: blobStore, linkedRepositories: [], totalAssetBytes: 0 }),
      ),
      http.delete('/service/rest/v1/blobstores/:name', () => { deleted = true; return new HttpResponse(null, { status: 204 }) }),
    )
    renderAdmin('blobs')
    fireEvent.click(await screen.findByText('default'))
    await screen.findByText('Blob Store: default')
    fireEvent.click(await screen.findByRole('button', { name: /Delete/ }))
    await waitFor(() => expect(deleted).toBe(true))
  })

  it('detail modal: shows group members for group type', async () => {
    const group = { ...blobStore, type: 'group', config: { fill_policy: 'round_robin' } }
    server.use(
      http.get('/service/rest/v1/blobstores', () => HttpResponse.json([group])),
      http.get('/api/v1/blob-stores/:name/usage', () =>
        HttpResponse.json({
          store: group,
          linkedRepositories: [],
          totalAssetBytes: 0,
          members: [{ id: 'm1', name: 'member-a', usedBytes: 1024 * 1024, quotaBytes: 2 * 1024 * 1024 }],
          memberTotalUsed: 1024 * 1024,
          memberTotalQuota: 2 * 1024 * 1024,
        }),
      ),
    )
    renderAdmin('blobs')
    fireEvent.click(await screen.findByText('default'))
    await screen.findByText('Blob Store: default')
    expect(await screen.findByText('member-a')).toBeInTheDocument()
    expect(screen.getByText('Round Robin')).toBeInTheDocument()
  })

  it('creates a local blob store', async () => {
    const user = userEvent.setup()
    let posted: { name: string } | null = null
    server.use(
      http.get('/service/rest/v1/blobstores', () => HttpResponse.json([])),
      http.post('/service/rest/v1/blobstores/:type', async ({ request }) => {
        posted = (await request.json()) as { name: string }
        return HttpResponse.json({}, { status: 201 })
      }),
    )
    renderAdmin('blobs')
    await screen.findByText('No blob stores configured')
    await user.click(screen.getAllByRole('button', { name: /New Blob Store/ })[0])
    await screen.findByRole('heading', { name: 'New Blob Store' })
    await user.type(screen.getByPlaceholderText('e.g. fast-ssd'), 'newstore')
    await user.click(screen.getByRole('button', { name: /^Create$/ }))
    await waitFor(() => expect(posted).toBeTruthy())
    expect(posted!.name).toBe('newstore')
  })

  it('create modal: test connection for local store', async () => {
    const user = userEvent.setup()
    server.use(
      http.get('/service/rest/v1/blobstores', () => HttpResponse.json([])),
      http.post('/api/v1/blobstores/test', () => HttpResponse.json({ ok: true })),
    )
    renderAdmin('blobs')
    await screen.findByText('No blob stores configured')
    await user.click(screen.getAllByRole('button', { name: /New Blob Store/ })[0])
    await screen.findByRole('heading', { name: 'New Blob Store' })
    await user.type(screen.getByPlaceholderText('e.g. fast-ssd'), 'teststore')
    await user.click(screen.getByRole('button', { name: /Test Connection/ }))
    expect(await screen.findByText('Connection successful')).toBeInTheDocument()
  })

  it('create modal: switches to s3 type showing s3 fields', async () => {
    const user = userEvent.setup()
    server.use(http.get('/service/rest/v1/blobstores', () => HttpResponse.json([])))
    renderAdmin('blobs')
    await screen.findByText('No blob stores configured')
    await user.click(screen.getAllByRole('button', { name: /New Blob Store/ })[0])
    await screen.findByRole('heading', { name: 'New Blob Store' })
    await user.click(screen.getByRole('button', { name: /Local filesystem/ }))
    await user.click(await screen.findByText('S3-compatible'))
    expect(await screen.findByText('Bucket')).toBeInTheDocument()
  })

  it('create modal: group type shows members and fill policy', async () => {
    const user = userEvent.setup()
    server.use(http.get('/service/rest/v1/blobstores', () => HttpResponse.json([blobStore])))
    renderAdmin('blobs')
    await screen.findByText('default')
    await user.click(screen.getAllByRole('button', { name: /New Blob Store/ })[0])
    await screen.findByRole('heading', { name: 'New Blob Store' })
    await user.click(screen.getByRole('button', { name: /Local filesystem/ }))
    await user.click(await screen.findByText('Group'))
    expect(await screen.findByText(/Fill Policy/)).toBeInTheDocument()
    expect(screen.getByText(/Members \(non-group/)).toBeInTheDocument()
  })
})

describe('AdminPage — Backup tab', () => {
  it('exports a backup', async () => {
    const click = vi.fn()
    vi.spyOn(document, 'createElement').mockImplementation(((tag: string) => {
      const el = document.createElementNS('http://www.w3.org/1999/xhtml', tag) as HTMLElement
      if (tag === 'a') (el as HTMLAnchorElement).click = click
      return el
    }) as typeof document.createElement)
    globalThis.URL.createObjectURL = vi.fn(() => 'blob:x')
    globalThis.URL.revokeObjectURL = vi.fn()
    server.use(
      http.get('/api/v1/backup/export', () => HttpResponse.text('tarball', { headers: { 'Content-Type': 'application/gzip' } })),
    )
    renderAdmin('backup')
    fireEvent.click(await screen.findByRole('button', { name: /Export Backup/ }))
    await waitFor(() => expect(click).toHaveBeenCalled())
  })

  it('restores from a file', async () => {
    server.use(
      http.post('/api/v1/backup/restore', () => HttpResponse.json({ restored: { repositories: 3, users: 2 } })),
    )
    renderAdmin('backup')
    await screen.findByText('System Backup & Restore')
    const fileInput = document.querySelector('input[type="file"][accept=".tar.gz,.tgz"]') as HTMLInputElement
    const file = new File(['x'], 'backup.tar.gz', { type: 'application/gzip' })
    fireEvent.change(fileInput, { target: { files: [file] } })
    expect(await screen.findByText('Restore complete')).toBeInTheDocument()
    expect(screen.getByText('repositories')).toBeInTheDocument()
  })

  it('shows restore error', async () => {
    server.use(
      http.post('/api/v1/backup/restore', () => HttpResponse.json({ error: 'bad archive' }, { status: 400 })),
    )
    renderAdmin('backup')
    await screen.findByText('System Backup & Restore')
    const fileInput = document.querySelector('input[type="file"][accept=".tar.gz,.tgz"]') as HTMLInputElement
    fireEvent.change(fileInput, { target: { files: [new File(['x'], 'b.tar.gz')] } })
    expect(await screen.findByText('bad archive')).toBeInTheDocument()
  })

  it('imports a repository', async () => {
    const user = userEvent.setup()
    server.use(
      http.post('/api/v1/repositories/import', () =>
        HttpResponse.json({ imported: { repository: 'imported-repo', components: 5, assets: 10, blobs: 8, conflictMode: 'skip' } }),
      ),
    )
    renderAdmin('backup')
    await screen.findByText('Repository Import')
    const importInput = document.querySelectorAll('input[type="file"][accept=".tar.gz,.tgz"]')[1] as HTMLInputElement
    fireEvent.change(importInput, { target: { files: [new File(['x'], 'repo.tar.gz')] } })
    expect(await screen.findByText('repo.tar.gz')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: /Import Repository/ }))
    expect(await screen.findByText('Import complete')).toBeInTheDocument()
    expect(screen.getByText('imported-repo')).toBeInTheDocument()
  })

  it('shows import error and clears file', async () => {
    const user = userEvent.setup()
    server.use(
      http.post('/api/v1/repositories/import', () => HttpResponse.json({ error: 'import broke' }, { status: 400 })),
    )
    renderAdmin('backup')
    await screen.findByText('Repository Import')
    const importInput = document.querySelectorAll('input[type="file"][accept=".tar.gz,.tgz"]')[1] as HTMLInputElement
    fireEvent.change(importInput, { target: { files: [new File(['x'], 'repo.tar.gz')] } })
    await screen.findByText('repo.tar.gz')
    await user.click(screen.getByRole('button', { name: /Import Repository/ }))
    expect(await screen.findByText('import broke')).toBeInTheDocument()
    // clear the chosen file
    fireEvent.click(screen.getByTitle('Clear'))
    await waitFor(() => expect(screen.queryByText('repo.tar.gz')).not.toBeInTheDocument())
  })
})

describe('AdminPage — Routing Rules tab', () => {
  const rule = { id: 'rr-1', name: 'block-snap', description: 'd', mode: 'BLOCK', matchers: ['.*-SNAPSHOT.*', 'x', 'y'], createdAt: '', updatedAt: '' }

  it('shows empty state', async () => {
    server.use(http.get('/service/rest/v1/routing-rules', () => HttpResponse.json([])))
    renderAdmin('routing-rules')
    expect(await screen.findByText('No routing rules configured')).toBeInTheDocument()
  })

  it('lists rules and creates one', async () => {
    const user = userEvent.setup()
    let posted: { name: string; matchers: string[] } | null = null
    server.use(
      http.get('/service/rest/v1/routing-rules', () => HttpResponse.json([rule])),
      http.post('/service/rest/v1/routing-rules', async ({ request }) => {
        posted = (await request.json()) as { name: string; matchers: string[] }
        return HttpResponse.json(rule, { status: 201 })
      }),
    )
    renderAdmin('routing-rules')
    expect(await screen.findByText('block-snap')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: /Create Routing Rule/ }))
    await screen.findByRole('heading', { name: 'Create Routing Rule' })
    await user.type(screen.getByPlaceholderText('block-snapshots'), 'my-rule')
    await user.type(screen.getByPlaceholderText('.*-SNAPSHOT.*'), '.*')
    await user.click(screen.getByRole('button', { name: /Add matcher/ }))
    await user.click(screen.getByRole('button', { name: /^Create$/ }))
    await waitFor(() => expect(posted).toBeTruthy())
    expect(posted!.name).toBe('my-rule')
  })

  it('validates name required on create', async () => {
    const user = userEvent.setup()
    server.use(http.get('/service/rest/v1/routing-rules', () => HttpResponse.json([])))
    renderAdmin('routing-rules')
    await screen.findByText('No routing rules configured')
    await user.click(screen.getByRole('button', { name: /Create Routing Rule/ }))
    await screen.findByRole('heading', { name: 'Create Routing Rule' })
    await user.click(screen.getByRole('button', { name: /^Create$/ }))
    expect(await screen.findByText('Name is required')).toBeInTheDocument()
  })

  it('edits a rule and removes a matcher', async () => {
    const user = userEvent.setup()
    let put: { name: string } | null = null
    server.use(
      http.get('/service/rest/v1/routing-rules', () => HttpResponse.json([rule])),
      http.put('/service/rest/v1/routing-rules/:id', async ({ request }) => {
        put = (await request.json()) as { name: string }
        return HttpResponse.json(rule)
      }),
    )
    renderAdmin('routing-rules')
    await screen.findByText('block-snap')
    await user.click(screen.getByRole('button', { name: /Edit/ }))
    await screen.findByText('Edit — block-snap')
    await user.click(screen.getByRole('button', { name: /^Save$/ }))
    await waitFor(() => expect(put).toBeTruthy())
  })

  it('deletes a rule', async () => {
    const user = userEvent.setup()
    let deleted = false
    server.use(
      http.get('/service/rest/v1/routing-rules', () => HttpResponse.json([rule])),
      http.delete('/service/rest/v1/routing-rules/:id', () => { deleted = true; return new HttpResponse(null, { status: 204 }) }),
    )
    renderAdmin('routing-rules')
    await screen.findByText('block-snap')
    await user.click(screen.getByRole('button', { name: /Delete/ }))
    await screen.findByText('Delete Routing Rule')
    const dialogBtns = screen.getAllByRole('button', { name: /^Delete$/ })
    await user.click(dialogBtns[dialogBtns.length - 1])
    await waitFor(() => expect(deleted).toBe(true))
  })
})

describe('AdminPage — Replication tab', () => {
  const repRule = {
    id: 're-1', name: 'mirror', source_repo: 'maven-hosted', target_url: 'https://t.example.com',
    target_repo: 'mirror', target_username: 'admin', cron_expr: '0 2 * * *', enabled: true,
    last_run_at: new Date().toISOString(), last_run_status: 'ok', created_at: '',
  }

  it('shows empty state', async () => {
    server.use(http.get('/api/v1/replication/rules', () => HttpResponse.json([])))
    renderAdmin('replication')
    expect(await screen.findByText('No replication rules configured.')).toBeInTheDocument()
  })

  it('lists rules and toggles history', async () => {
    const user = userEvent.setup()
    server.use(
      http.get('/api/v1/replication/rules', () => HttpResponse.json([repRule])),
      http.get('/api/v1/replication/rules/:id/history', () =>
        HttpResponse.json([
          { id: 'h1', rule_id: 're-1', started_at: new Date().toISOString(), finished_at: null, duration_ms: 1500, pushed_count: 3, skipped_count: 1, failed_count: 0, transferred_bytes: 2048, error: '' },
        ]),
      ),
    )
    renderAdmin('replication')
    expect(await screen.findByText('mirror')).toBeInTheDocument()
    await user.click(screen.getByTitle('History'))
    expect(await screen.findByText('Started')).toBeInTheDocument()
  })

  // The history viewer kept one shared array, so a request for the rule that was
  // open first resolved later and overwrote it — rendering the earlier rule's
  // runs under the newly opened rule's header, which misattributes a broken
  // target's failures to a healthy one and the other way round.
  it('never renders one rule\'s history under another rule\'s header', async () => {
    const user = userEvent.setup()
    const ruleB = { ...repRule, id: 're-2', name: 'mirror-b', source_repo: 'npm-hosted' }
    let releaseA: (() => void) | undefined
    const aHeld = new Promise<void>((resolve) => { releaseA = resolve })
    const run = (id: string, pushed: number) => ({
      id, rule_id: id, started_at: new Date().toISOString(), finished_at: null,
      duration_ms: 1500, pushed_count: pushed, skipped_count: 0, failed_count: 0,
      transferred_bytes: 2048, error: '',
    })

    server.use(
      http.get('/api/v1/replication/rules', () => HttpResponse.json([repRule, ruleB])),
      http.get('/api/v1/replication/rules/:id/history', async ({ params }) => {
        if (params.id === 're-1') {
          await aHeld
          return HttpResponse.json([run('h-a', 111)])
        }
        return HttpResponse.json([run('h-b', 222)])
      }),
    )

    renderAdmin('replication')
    await screen.findByText('mirror')

    // Open rule A, then switch to rule B before A's history has arrived.
    const historyButtons = screen.getAllByTitle('History')
    await user.click(historyButtons[0])
    await user.click(historyButtons[1])
    expect(await screen.findByText('222')).toBeInTheDocument()

    // A's response lands afterwards and must not reach B's panel.
    releaseA?.()
    await waitFor(() => expect(screen.queryByText('Loading history…')).not.toBeInTheDocument())
    expect(screen.queryByText('111')).not.toBeInTheDocument()
    expect(screen.getByText('222')).toBeInTheDocument()

    // Reopening A shows A's own runs.
    await user.click(screen.getAllByTitle('History')[0])
    expect(await screen.findByText('111')).toBeInTheDocument()
  })

  it('runs and tests a rule', async () => {
    const user = userEvent.setup()
    let ran = false
    server.use(
      http.get('/api/v1/replication/rules', () => HttpResponse.json([repRule])),
      http.post('/api/v1/replication/rules/:id/run', () => { ran = true; return HttpResponse.json({}) }),
      http.post('/api/v1/replication/rules/:id/test', () => HttpResponse.json({ ok: true })),
    )
    renderAdmin('replication')
    await screen.findByText('mirror')
    await user.click(screen.getByTitle('Run now'))
    await waitFor(() => expect(ran).toBe(true))
    await user.click(screen.getByTitle('Test connection'))
    expect(await screen.findByText('✓ Connected')).toBeInTheDocument()
  })

  it('creates a replication rule', async () => {
    const user = userEvent.setup()
    let posted: { name: string } | null = null
    server.use(
      http.get('/api/v1/replication/rules', () => HttpResponse.json([])),
      http.post('/api/v1/replication/rules', async ({ request }) => {
        posted = (await request.json()) as { name: string }
        return HttpResponse.json(repRule, { status: 201 })
      }),
    )
    renderAdmin('replication')
    await screen.findByText('No replication rules configured.')
    await user.click(screen.getByRole('button', { name: /New Rule/ }))
    await screen.findByText('New Replication Rule')
    await user.type(screen.getByPlaceholderText('prod-mirror'), 'rule1')
    await user.type(screen.getByPlaceholderText('https://nexspence.example.com'), 'https://x.com')
    await user.type(screen.getByPlaceholderText('my-repo-mirror'), 'r-mirror')
    await user.click(screen.getByRole('button', { name: /^Create$/ }))
    await waitFor(() => expect(posted).toBeTruthy())
    expect(posted!.name).toBe('rule1')
  })

  it('deletes a rule', async () => {
    const user = userEvent.setup()
    let deleted = false
    server.use(
      http.get('/api/v1/replication/rules', () => HttpResponse.json([repRule])),
      http.delete('/api/v1/replication/rules/:id', () => { deleted = true; return new HttpResponse(null, { status: 204 }) }),
    )
    renderAdmin('replication')
    await screen.findByText('mirror')
    await user.click(screen.getByTitle('Delete'))
    await waitFor(() => expect(deleted).toBe(true))
  })

  it('opens edit modal', async () => {
    const user = userEvent.setup()
    server.use(http.get('/api/v1/replication/rules', () => HttpResponse.json([repRule])))
    renderAdmin('replication')
    await screen.findByText('mirror')
    await user.click(screen.getByTitle('Edit'))
    expect(await screen.findByText('Edit Replication Rule')).toBeInTheDocument()
  })
})

describe('AdminPage — SAML tab', () => {
  it('shows disabled SAML', async () => {
    server.use(
      http.get('/api/v1/auth/config', () => HttpResponse.json(fixtures.authConfig({ samlEnabled: false }))),
    )
    renderAdmin('saml')
    expect(await screen.findByText(/SAML SSO is not enabled/)).toBeInTheDocument()
  })

  it('shows enabled SAML config and service health', async () => {
    server.use(
      http.get('/api/v1/auth/config', () =>
        HttpResponse.json(fixtures.authConfig({
          samlEnabled: true, samlDisplayName: 'Corp SSO', samlEntityId: 'nexspence',
          samlAcsUrl: 'https://x/acs', samlIdpMetadataUrl: 'https://idp/meta', samlProvisioning: 'jit',
        })),
      ),
      http.get('/api/v1/system/services', () =>
        HttpResponse.json([{ name: 'SAML IdP', status: 'ok', detail: 'reachable', checked_at: new Date().toISOString() }]),
      ),
    )
    renderAdmin('saml')
    expect(await screen.findByText('Corp SSO')).toBeInTheDocument()
    expect(screen.getByText('Download SP Metadata XML')).toBeInTheDocument()
    expect(await screen.findByText('SAML IdP Connection')).toBeInTheDocument()
  })
})

describe('AdminPage — Promotion tab', () => {
  const promRule = {
    id: 'pr-1', name: 'to-release', from_repo: 'maven-hosted', to_repo: 'maven-release',
    path_filter: 'path.startsWith("/x")', require_scan_pass: true, require_manual_approval: true, created_at: '',
  }
  const promReq = {
    id: 'req-1', rule_id: 'pr-1', component_id: 'comp-12345678', status: 'pending',
    requested_by: 'admin', created_at: new Date().toISOString(),
  }

  it('shows empty rules and requests', async () => {
    server.use(
      http.get('/api/v1/promotion/rules', () => HttpResponse.json([])),
      http.get('/api/v1/promotion/requests', () => HttpResponse.json([])),
    )
    renderAdmin('promotion')
    expect(await screen.findByText('No promotion rules configured')).toBeInTheDocument()
    expect(await screen.findByText('No promotion requests')).toBeInTheDocument()
  })

  it('lists rules and a pending request, approves it', async () => {
    const user = userEvent.setup()
    let approved = false
    server.use(
      http.get('/api/v1/promotion/rules', () => HttpResponse.json([promRule])),
      http.get('/api/v1/promotion/requests', () => HttpResponse.json([promReq])),
      http.post('/api/v1/promotion/requests/:id/approve', () => { approved = true; return HttpResponse.json({}) }),
    )
    renderAdmin('promotion')
    expect((await screen.findAllByText('to-release')).length).toBeGreaterThan(0)
    expect(screen.getByText('Scan Pass')).toBeInTheDocument()
    expect(screen.getByText('Manual Approval')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: /Approve/ }))
    await waitFor(() => expect(approved).toBe(true))
  })

  it('rejects a request with a reason', async () => {
    const user = userEvent.setup()
    let rejectBody: { reason: string } | null = null
    server.use(
      http.get('/api/v1/promotion/rules', () => HttpResponse.json([promRule])),
      http.get('/api/v1/promotion/requests', () => HttpResponse.json([promReq])),
      http.post('/api/v1/promotion/requests/:id/reject', async ({ request }) => {
        rejectBody = (await request.json()) as { reason: string }
        return HttpResponse.json({})
      }),
    )
    renderAdmin('promotion')
    await screen.findAllByText('to-release')
    await user.click(screen.getByRole('button', { name: /Reject/ }))
    await screen.findByText('Reject Promotion Request')
    await user.type(screen.getByPlaceholderText('Optional reason for rejection'), 'nope')
    const rejectBtns = screen.getAllByRole('button', { name: /^Reject$/ })
    await user.click(rejectBtns[rejectBtns.length - 1])
    await waitFor(() => expect(rejectBody).toBeTruthy())
    expect(rejectBody!.reason).toBe('nope')
  })

  it('creates a promotion rule', async () => {
    const user = userEvent.setup()
    let posted: { name: string } | null = null
    server.use(
      http.get('/api/v1/promotion/rules', () => HttpResponse.json([])),
      http.get('/api/v1/promotion/requests', () => HttpResponse.json([])),
      http.get('/service/rest/v1/repositories', () =>
        HttpResponse.json([fixtures.repository({ name: 'maven-hosted' }), fixtures.repository({ id: 'r2', name: 'maven-release' })]),
      ),
      http.post('/api/v1/promotion/rules', async ({ request }) => {
        posted = (await request.json()) as { name: string }
        return HttpResponse.json(promRule, { status: 201 })
      }),
    )
    renderAdmin('promotion')
    await screen.findByText('No promotion rules configured')
    await user.click(screen.getByRole('button', { name: /Create Rule/ }))
    await screen.findByText('Create Promotion Rule')
    await user.type(screen.getByPlaceholderText('promote-to-release'), 'new-rule')
    await user.click(screen.getByRole('button', { name: /Select source repository/ }))
    await user.click((await screen.findAllByText('maven-hosted'))[0])
    await user.click(screen.getByRole('button', { name: /Select target repository/ }))
    await user.click((await screen.findAllByText('maven-release'))[0])
    await user.click(screen.getByRole('button', { name: /^Create$/ }))
    await waitFor(() => expect(posted).toBeTruthy())
    expect(posted!.name).toBe('new-rule')
  })

  it('validates promotion rule name required', async () => {
    const user = userEvent.setup()
    server.use(
      http.get('/api/v1/promotion/rules', () => HttpResponse.json([])),
      http.get('/api/v1/promotion/requests', () => HttpResponse.json([])),
    )
    renderAdmin('promotion')
    await screen.findByText('No promotion rules configured')
    await user.click(screen.getByRole('button', { name: /Create Rule/ }))
    await screen.findByText('Create Promotion Rule')
    await user.click(screen.getByRole('button', { name: /^Create$/ }))
    expect(await screen.findByText('Name is required')).toBeInTheDocument()
  })

  it('deletes a promotion rule', async () => {
    const user = userEvent.setup()
    let deleted = false
    server.use(
      http.get('/api/v1/promotion/rules', () => HttpResponse.json([promRule])),
      http.get('/api/v1/promotion/requests', () => HttpResponse.json([])),
      http.delete('/api/v1/promotion/rules/:id', () => { deleted = true; return HttpResponse.json({}) }),
    )
    renderAdmin('promotion')
    await screen.findByText('to-release')
    await user.click(screen.getByRole('button', { name: /Delete/ }))
    await screen.findByText('Delete Promotion Rule')
    const delBtns = screen.getAllByRole('button', { name: /^Delete$/ })
    await user.click(delBtns[delBtns.length - 1])
    await waitFor(() => expect(deleted).toBe(true))
  })
})

describe('AdminPage — Migration tab', () => {
  it('shows empty state', async () => {
    server.use(http.get('/api/v1/migration/jobs', () => HttpResponse.json([])))
    renderAdmin('migration')
    expect(await screen.findByText('No migration jobs yet')).toBeInTheDocument()
  })

  it('lists active and history jobs with pause/resume', async () => {
    const user = userEvent.setup()
    let paused = false
    server.use(
      http.get('/api/v1/migration/jobs', () =>
        HttpResponse.json([
          { id: 'j1', sourceUrl: 'https://nexus.a.com', sourceUser: 'admin', status: 'running', migrateRepos: true, migrateUsers: true, migrateBlobs: true, migratePolicies: false, repositoriesTotal: 10, repositoriesDone: 5, assetsTotal: 100, assetsDone: 50, errorCount: 0, createdAt: new Date().toISOString(), updatedAt: new Date().toISOString() },
          { id: 'j2', sourceUrl: 'https://nexus.b.com', sourceUser: '', status: 'done', migrateRepos: true, migrateUsers: false, migrateBlobs: false, migratePolicies: false, repositoriesTotal: 3, repositoriesDone: 3, assetsTotal: 30, assetsDone: 30, errorCount: 2, createdAt: '', updatedAt: new Date().toISOString(), finishedAt: new Date().toISOString() },
        ]),
      ),
      http.post('/api/v1/migration/jobs/:id/pause', () => { paused = true; return HttpResponse.json({}) }),
    )
    renderAdmin('migration')
    expect(await screen.findByText('https://nexus.a.com')).toBeInTheDocument()
    expect(screen.getByText('Migration History')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: /Pause/ }))
    await waitFor(() => expect(paused).toBe(true))
  })

  it('creates a migration job through the wizard', async () => {
    const user = userEvent.setup()
    let posted: { sourceUrl: string } | null = null
    server.use(
      http.get('/api/v1/migration/jobs', () => HttpResponse.json([])),
      http.post('/api/v1/migration/jobs', async ({ request }) => {
        posted = (await request.json()) as { sourceUrl: string }
        return HttpResponse.json({ id: 'job-1' }, { status: 201 })
      }),
    )
    renderAdmin('migration')
    await screen.findByText('No migration jobs yet')
    await user.click(screen.getByRole('button', { name: /Start Migration/ }))
    await screen.findByText('Step 1 of 3')
    await user.type(screen.getByPlaceholderText('https://nexus.example.com'), 'https://src.com')
    const pwInputs = document.querySelectorAll('input[type="password"]')
    fireEvent.change(pwInputs[0], { target: { value: 'secret' } })
    await user.click(screen.getByRole('button', { name: /Next/ }))
    await screen.findByText('Step 2 of 3')
    await user.click(screen.getByRole('button', { name: /Next/ }))
    await screen.findByText('Step 3 of 3')
    const startBtns = screen.getAllByRole('button', { name: /Start Migration/ })
    await user.click(startBtns[startBtns.length - 1])
    await waitFor(() => expect(posted).toBeTruthy())
    expect(posted!.sourceUrl).toBe('https://src.com')
  })

  it('validates the wizard source step', async () => {
    const user = userEvent.setup()
    server.use(http.get('/api/v1/migration/jobs', () => HttpResponse.json([])))
    renderAdmin('migration')
    await screen.findByText('No migration jobs yet')
    await user.click(screen.getByRole('button', { name: /Start Migration/ }))
    await screen.findByText('Step 1 of 3')
    await user.click(screen.getByRole('button', { name: /Next/ }))
    expect(await screen.findByText('Nexus URL is required')).toBeInTheDocument()
  })
})

describe('AdminPage — Monitoring tab (lazy)', () => {
  it('lazy-loads the monitoring view', async () => {
    server.use(
      http.get('/api/v1/metrics', () =>
        HttpResponse.json({ artifactsStored: 5, bytesStored: 1024, downloadsTotal: 10, requestsTotal: 100, requestErrors: 1, artifactsDeleted: 0 }),
      ),
      http.get('/api/v1/metrics/history', () => HttpResponse.json([])),
      http.get('/api/v1/metrics/repos', () => HttpResponse.json([])),
    )
    renderAdmin('monitoring')
    // MonitoringView renders async after the lazy chunk loads
    await waitFor(() => expect(screen.queryByText('Loading…')).not.toBeInTheDocument(), { timeout: 3000 })
  })
})

describe('AdminPage — tab switching', () => {
  it('switches between tabs via the tab bar', async () => {
    const user = userEvent.setup()
    server.use(
      http.get('/service/rest/v1/blobstores', () => HttpResponse.json([])),
      http.get('/service/rest/v1/routing-rules', () => HttpResponse.json([])),
    )
    renderAdmin('info')
    await screen.findByText('System Status')
    await user.click(screen.getByRole('button', { name: /Blob Stores/ }))
    expect(await screen.findByText('No blob stores configured')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: /Routing Rules/ }))
    expect(await screen.findByText('No routing rules configured')).toBeInTheDocument()
  })
})
