import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { screen, waitFor, fireEvent, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { QueryClient } from '@tanstack/react-query'
import BrowsePage from './BrowsePage'
import { renderWithProviders, seedAuthAsAdmin, seedAuthAsGuest } from '@/test/renderUtils'
import { server } from '@/test/msw/server'
import { fixtures } from '@/test/fixtures'
import { useAuthStore } from '@/store/authStore'

const repos = [
  fixtures.repository({ id: 'r1', name: 'maven-hosted', format: 'maven2', type: 'hosted' }),
  fixtures.repository({ id: 'r2', name: 'docker-hosted', format: 'docker', type: 'hosted' }),
  fixtures.repository({ id: 'r3', name: 'raw-hosted', format: 'raw', type: 'hosted' }),
  fixtures.repository({ id: 'r4', name: 'oci-hosted', format: 'oci', type: 'hosted' }),
]

function renderBrowse(search = '', queryClient?: QueryClient) {
  return renderWithProviders(<BrowsePage />, {
    routerProps: { initialEntries: [`/browse${search}`] },
    queryClient,
  })
}

function seedRepos() {
  server.use(http.get('/service/rest/v1/repositories', () => HttpResponse.json(repos)))
}

beforeEach(() => {
  seedAuthAsAdmin()
  seedRepos()
})
afterEach(() => {
  vi.restoreAllMocks()
})

describe('BrowsePage — repo selector & empty states', () => {
  it('shows the initial no-repo prompt', async () => {
    renderBrowse()
    expect(await screen.findByText('Choose a repository above')).toBeInTheDocument()
    expect(screen.getByText('Select a repository to browse')).toBeInTheDocument()
  })

  it('selects a maven (component) repo and shows empty components', async () => {
    const user = userEvent.setup()
    server.use(
      http.get('/service/rest/v1/components', () => HttpResponse.json({ items: [], continuationToken: null })),
    )
    renderBrowse()
    await screen.findByText('Choose a repository above')
    await user.click(screen.getByRole('button', { name: /Select repository/ }))
    await user.click((await screen.findAllByText('maven-hosted'))[0])
    expect(await screen.findByText('No components in this repository')).toBeInTheDocument()
  })

  it('lists components with assets, bulk-selects and shows promote bar', async () => {
    const user = userEvent.setup()
    server.use(
      http.get('/service/rest/v1/components', () =>
        HttpResponse.json({
          items: [
            { id: 'c1', name: 'pkg-a', group: 'com.example', version: '1.0', format: 'maven2', assets: [{ id: 'a1', path: 'com/example/pkg-a/1.0/pkg-a.jar', fileSize: 2048, contentType: 'application/java-archive' }, { id: 'a2', path: 'x', fileSize: 1, contentType: 't' }] },
            { id: 'c2', name: 'pkg-b', group: '', version: '2.0', format: 'maven2', assets: [] },
          ],
          continuationToken: 'next',
        }),
      ),
      http.get('/api/v1/components/:id/promotion-rules', () =>
        HttpResponse.json([{ id: 'pr1', name: 'rel', from_repo: 'maven-hosted', to_repo: 'maven-release', require_scan_pass: false, require_manual_approval: false }]),
      ),
    )
    renderBrowse('?repo=maven-hosted')
    expect(await screen.findByText('pkg-a')).toBeInTheDocument()
    expect(screen.getByText('pkg-b')).toBeInTheDocument()
    // pagination shows next enabled
    expect(screen.getByText('Page 1')).toBeInTheDocument()
    // bulk select first row
    const checkboxes = screen.getAllByRole('checkbox')
    await user.click(checkboxes[0])
    expect(await screen.findByText('1 selected')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: /Promote selected/ }))
    expect(await screen.findByText(/Promote 1 component/)).toBeInTheDocument()
  })

  it('shows per-component size and push date from the embedded assets (#257)', async () => {
    server.use(
      http.get('/service/rest/v1/components', () =>
        HttpResponse.json({
          items: [
            {
              id: 'c1', name: 'pkg-a', group: '', version: '1.0', format: 'maven2',
              // Size must be the sum over all assets, the push date the newest
              // lastModified — not the first asset's values. The unparseable
              // timestamp comes first: were it carried as "best", every later
              // NaN comparison would keep it and no date would render.
              assets: [
                { id: 'a0', path: 'p/pkg-a.md5', fileSize: 0, contentType: 't', lastModified: 'not-a-date' },
                { id: 'a1', path: 'p/pkg-a.jar', fileSize: 2048, contentType: 't', lastModified: '2026-08-01T10:00:00Z' },
                { id: 'a2', path: 'p/pkg-a.pom', fileSize: 1024, contentType: 't', lastModified: '2026-08-17T12:30:00Z' },
              ],
            },
            { id: 'c2', name: 'pkg-b', group: '', version: '2.0', format: 'maven2', assets: [] },
          ],
          continuationToken: null,
        }),
      ),
    )
    renderBrowse('?repo=maven-hosted')
    expect(await screen.findByText('3.0 KB')).toBeInTheDocument()
    // Compared whitespace-normalized on both sides: Intl output may separate
    // time from AM/PM with U+202F, which the testing-library normalizer
    // collapses in the DOM text but not in the expected string.
    const norm = (s: string) => s.replace(/\s+/g, ' ')
    const pushed = norm(new Date('2026-08-17T12:30:00Z').toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' }))
    expect(screen.getByText((t) => norm(t) === pushed)).toBeInTheDocument()
    // A component with no assets shows placeholders — not "0 B", not a 1970
    // date from reducing over nothing.
    const pkgBRow = screen.getByText('pkg-b').closest('div[style]')?.parentElement
    expect(pkgBRow?.textContent).not.toContain('0 B')
    expect(pkgBRow?.textContent).not.toContain('1970')
    expect(pkgBRow?.textContent).toContain('—')
    expect(screen.getByText('Size')).toBeInTheDocument()
    expect(screen.getByText('Pushed')).toBeInTheDocument()
  })

  it('paginates next and prev', async () => {
    const user = userEvent.setup()
    let lastOffset = '0'
    server.use(
      http.get('/service/rest/v1/components', ({ request }) => {
        const url = new URL(request.url)
        lastOffset = url.searchParams.get('offset') ?? '0'
        return HttpResponse.json({
          items: [{ id: 'c1', name: 'pkg-a', group: '', version: '1', format: 'maven2', assets: [] }],
          continuationToken: 'next',
        })
      }),
    )
    renderBrowse('?repo=maven-hosted')
    await screen.findByText('pkg-a')
    await user.click(screen.getByText('Next →'))
    await waitFor(() => expect(lastOffset).toBe('25'))
    await user.click(screen.getByText('← Prev'))
    await waitFor(() => expect(lastOffset).toBe('0'))
  })

  it('shows access-denied on 403', async () => {
    server.use(
      http.get('/service/rest/v1/components', () => HttpResponse.json({ error: 'denied' }, { status: 403 })),
    )
    renderBrowse('?repo=maven-hosted')
    expect(await screen.findByText(/Access denied/)).toBeInTheDocument()
  })

  it('deletes a component asset by path after confirm', async () => {
    const user = userEvent.setup()
    let deleted = false
    server.use(
      http.get('/service/rest/v1/components', () =>
        HttpResponse.json({ items: [{ id: 'c1', name: 'pkg-a', group: '', version: '1', format: 'maven2', assets: [{ id: 'a1', path: 'p/pkg.jar', fileSize: 1, contentType: 't' }] }], continuationToken: null }),
      ),
      http.delete('/api/v1/browse/repositories/:name/path', () => { deleted = true; return new HttpResponse(null, { status: 204 }) }),
    )
    renderBrowse('?repo=maven-hosted')
    await screen.findByText('pkg-a')
    await user.click(screen.getByTitle('Delete'))
    await screen.findByText('Delete file?')
    const delBtns = screen.getAllByRole('button', { name: /^Delete$/ })
    await user.click(delBtns[delBtns.length - 1])
    await waitFor(() => expect(deleted).toBe(true))
  })

  // Regression for #75/#76: the row delete must target the asset path. When the
  // component name was used instead, the prefix matched nothing (npm: silent
  // no-op) or was empty (apt proxy: 400 with no server-side error).
  it('deletes using the asset path, not the component name', async () => {
    const user = userEvent.setup()
    const deletedPaths: string[] = []
    server.use(
      http.get('/service/rest/v1/components', () =>
        HttpResponse.json({
          items: [{
            id: 'c1', name: 'lodash', group: '', version: '4.17.21', format: 'npm',
            assets: [{ id: 'a1', path: '/lodash/-/lodash-4.17.21.tgz', fileSize: 1, contentType: 't' }],
          }],
          continuationToken: null,
        }),
      ),
      http.delete('/api/v1/browse/repositories/:name/path', ({ request }) => {
        deletedPaths.push(new URL(request.url).searchParams.get('path') ?? '')
        return new HttpResponse(null, { status: 204 })
      }),
    )
    renderBrowse('?repo=maven-hosted')
    await screen.findByText('lodash')
    await user.click(screen.getByTitle('Delete'))
    await screen.findByText('Delete file?')
    const delBtns = screen.getAllByRole('button', { name: /^Delete/ })
    await user.click(delBtns[delBtns.length - 1])
    await waitFor(() => expect(deletedPaths).toEqual(['/lodash/-/lodash-4.17.21.tgz']))
  })

  // An apt proxy stores every cached file under one component, so deleting the
  // row must remove all of its assets — not just the first one.
  it('deletes every asset of a multi-asset component', async () => {
    const user = userEvent.setup()
    const deletedPaths: string[] = []
    server.use(
      http.get('/service/rest/v1/components', () =>
        HttpResponse.json({
          items: [{
            id: 'c1', name: 'nginx', group: '', version: '1.24', format: 'apt',
            assets: [
              { id: 'a1', path: '/pool/main/n/nginx/nginx_1.24_amd64.deb', fileSize: 1, contentType: 't' },
              { id: 'a2', path: '/dists/trixie/InRelease', fileSize: 1, contentType: 't' },
            ],
          }],
          continuationToken: null,
        }),
      ),
      http.delete('/api/v1/browse/repositories/:name/path', ({ request }) => {
        deletedPaths.push(new URL(request.url).searchParams.get('path') ?? '')
        return new HttpResponse(null, { status: 204 })
      }),
    )
    renderBrowse('?repo=maven-hosted')
    await screen.findByText('nginx')
    await user.click(screen.getByTitle('Delete'))
    await screen.findByText('Delete component?')
    const delBtns = screen.getAllByRole('button', { name: /^Delete/ })
    await user.click(delBtns[delBtns.length - 1])
    await waitFor(() => expect(deletedPaths.sort()).toEqual([
      '/dists/trixie/InRelease',
      '/pool/main/n/nginx/nginx_1.24_amd64.deb',
    ]))
  })

  // A component with no assets has nothing to delete: report it instead of
  // firing a request that the server rejects with an unexplained 400.
  it('reports an error instead of deleting a component that has no assets', async () => {
    const user = userEvent.setup()
    let called = false
    server.use(
      http.get('/service/rest/v1/components', () =>
        HttpResponse.json({
          items: [{ id: 'c1', name: 'ghost', group: '', version: '1', format: 'npm', assets: [] }],
          continuationToken: null,
        }),
      ),
      http.delete('/api/v1/browse/repositories/:name/path', () => {
        called = true
        return new HttpResponse(null, { status: 204 })
      }),
    )
    renderBrowse('?repo=maven-hosted')
    await screen.findByText('ghost')
    await user.click(screen.getByTitle('Delete'))
    await screen.findByText('Delete file?')
    const delBtns = screen.getAllByRole('button', { name: /^Delete/ })
    await user.click(delBtns[delBtns.length - 1])
    expect(await screen.findByText(/no assets to delete/i)).toBeInTheDocument()
    expect(called).toBe(false)
  })
})

describe('BrowsePage — Raw tree', () => {
  const rawTree = {
    root: {
      kind: 'folder', label: '', path: '', children: [
        {
          kind: 'folder', label: 'releases', path: '/releases', children: [
            { kind: 'file', label: 'app.tar.gz', path: '/releases/app.tar.gz', size: 4096, sha256: 'abc123', contentType: 'application/gzip', updatedAt: new Date().toISOString(), componentId: 'comp-raw-1' },
          ],
        },
      ],
    },
  }

  function seedRaw() {
    server.use(
      http.get('/api/v1/browse/repositories/:name/raw-tree', () => HttpResponse.json(rawTree)),
      http.get('/service/rest/v1/components/:id', () => HttpResponse.json({ tags: ['stable'] })),
    )
  }

  it('shows empty raw tree', async () => {
    server.use(http.get('/api/v1/browse/repositories/:name/raw-tree', () => HttpResponse.json({ root: { kind: 'folder', label: '', path: '', children: [] } })))
    renderBrowse('?repo=raw-hosted')
    expect(await screen.findByText('No files in this repository yet')).toBeInTheDocument()
  })

  it('expands folder, selects file, shows detail panel', async () => {
    const user = userEvent.setup()
    seedRaw()
    renderBrowse('?repo=raw-hosted')
    await user.click(await screen.findByText('releases'))
    await user.click(await screen.findByText('app.tar.gz'))
    expect(await screen.findByText('File details')).toBeInTheDocument()
    expect(screen.getByText('abc123')).toBeInTheDocument()
    expect(screen.getByText('SHA256')).toBeInTheDocument()
    // tag editor section loaded
    expect(await screen.findByText('stable')).toBeInTheDocument()
  })

  it('downloads, copies link and opens usage from detail panel', async () => {
    const user = userEvent.setup()
    seedRaw()
    const writeText = vi.fn()
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    Object.defineProperty(window, 'location', { value: { ...window.location, origin: 'http://localhost' }, configurable: true })
    const click = vi.fn()
    vi.spyOn(document, 'createElement').mockImplementation(((tag: string) => {
      const el = document.createElementNS('http://www.w3.org/1999/xhtml', tag) as HTMLElement
      if (tag === 'a') (el as HTMLAnchorElement).click = click
      return el
    }) as typeof document.createElement)
    globalThis.URL.createObjectURL = vi.fn(() => 'blob:x')
    globalThis.URL.revokeObjectURL = vi.fn()
    server.use(http.get('/repository/:name/*', () => HttpResponse.text('data')))
    renderBrowse('?repo=raw-hosted')
    await user.click(await screen.findByText('releases'))
    await user.click(await screen.findByText('app.tar.gz'))
    const panel = (await screen.findByText('File details')).closest('.holo-card') as HTMLElement
    await user.click(within(panel).getByRole('button', { name: /Download/ }))
    await waitFor(() => expect(click).toHaveBeenCalled())
    await user.click(within(panel).getByRole('button', { name: /Copy link/ }))
    expect(writeText).toHaveBeenCalled()
    await user.click(within(panel).getByRole('button', { name: /Usage/ }))
    expect(await screen.findByText('Example Usage')).toBeInTheDocument()
  })

  it('downloads via the hover row buttons', async () => {
    const user = userEvent.setup()
    seedRaw()
    const writeText = vi.fn()
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    globalThis.URL.createObjectURL = vi.fn(() => 'blob:x')
    globalThis.URL.revokeObjectURL = vi.fn()
    server.use(http.get('/repository/:name/*', () => HttpResponse.text('data')))
    renderBrowse('?repo=raw-hosted')
    await user.click(await screen.findByText('releases'))
    const fileRow = (await screen.findByText('app.tar.gz')).closest('[role="button"]')!
    fireEvent.mouseEnter(fileRow)
    fireEvent.click(within(fileRow as HTMLElement).getByTitle('Copy link'))
    expect(writeText).toHaveBeenCalled()
  })

  it('deletes a raw file via hover delete', async () => {
    const user = userEvent.setup()
    let deleted = false
    seedRaw()
    server.use(
      http.delete('/api/v1/browse/repositories/:name/path', () => { deleted = true; return new HttpResponse(null, { status: 204 }) }),
    )
    renderBrowse('?repo=raw-hosted')
    await user.click(await screen.findByText('releases'))
    const fileRow = (await screen.findByText('app.tar.gz')).closest('[role="button"]')!
    fireEvent.mouseEnter(fileRow)
    fireEvent.click(within(fileRow as HTMLElement).getByTitle('Delete'))
    await screen.findByText('Delete file?')
    const delBtns = screen.getAllByRole('button', { name: /^Delete$/ })
    await user.click(delBtns[delBtns.length - 1])
    await waitFor(() => expect(deleted).toBe(true))
  })

  it('deletes a raw folder (shows affected paths)', async () => {
    const user = userEvent.setup()
    let deleted = false
    seedRaw()
    server.use(
      http.delete('/api/v1/browse/repositories/:name/path', () => { deleted = true; return new HttpResponse(null, { status: 204 }) }),
    )
    renderBrowse('?repo=raw-hosted')
    const folderRow = (await screen.findByText('releases')).closest('div')!
    fireEvent.mouseEnter(folderRow)
    fireEvent.click(within(folderRow).getByTitle(/Delete folder/))
    expect(await screen.findByText('Delete folder?')).toBeInTheDocument()
    expect(screen.getByText(/files affected/)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: /Delete 1 files/ }))
    await waitFor(() => expect(deleted).toBe(true))
  })

  it('shows the upload modal for hosted raw repos and uploads', async () => {
    const user = userEvent.setup()
    seedRaw()
    renderBrowse('?repo=raw-hosted')
    await screen.findByText('releases')
    await user.click(screen.getByRole('button', { name: /Upload/ }))
    expect(await screen.findByText('Upload file')).toBeInTheDocument()
    // close it
    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByText('Upload file')).not.toBeInTheDocument())
  })
})

describe('BrowsePage — Docker tree', () => {
  const dockerTree = {
    root: {
      kind: 'folder', label: '', path: '', children: [
        {
          kind: 'folder', label: 'myapp', path: '/myapp', imageRef: 'myapp', children: [
            {
              kind: 'folder', label: 'Tags', path: '/myapp/Tags', children: [
                { kind: 'tag', label: 'latest', path: '/myapp/Tags/latest', imageRef: 'myapp', version: 'latest', componentId: 'dc1' },
                { kind: 'tag', label: 'stable', path: '/myapp/Tags/stable', imageRef: 'myapp', version: 'stable', componentId: 'dc2' },
              ],
            },
          ],
        },
      ],
    },
  }

  const dockerDetail = {
    id: 'dc1', repository: 'docker-hosted', format: 'docker', name: 'myapp', version: 'latest', group: '',
    createdAt: new Date().toISOString(), tags: ['prod'],
    assets: [{ path: 'v2/myapp/manifests/latest', fileSize: 512, contentType: 'application/vnd.docker.distribution.manifest.v2+json', createdAt: new Date().toISOString(), lastModified: new Date().toISOString(), blobKey: 'sha256:xyz', blobStoreId: 'bs1', uploader: 'admin' }],
  }

  function seedDocker() {
    server.use(
      http.get('/api/v1/browse/repositories/:name/docker-tree', () => HttpResponse.json(dockerTree)),
      // The detail follows the requested id, so selecting a second tag really
      // changes what the panel is describing.
      http.get('/service/rest/v1/components/:id', ({ params }) =>
        HttpResponse.json(
          params.id === 'dc1'
            ? dockerDetail
            : { ...dockerDetail, id: String(params.id), version: 'stable' },
        )),
    )
  }

  it('shows empty docker tree', async () => {
    server.use(http.get('/api/v1/browse/repositories/:name/docker-tree', () => HttpResponse.json({ root: { kind: 'folder', label: '', path: '', children: [] } })))
    renderBrowse('?repo=docker-hosted')
    expect(await screen.findByText(/No Docker metadata cached yet/)).toBeInTheDocument()
  })

  // A signature or SBOM only points at the image it describes, so the panel is
  // the only place they can be shown grouped under it (#199).
  it('lists referrers of the selected tag with their friendly labels', async () => {
    const user = userEvent.setup()
    seedDocker()
    server.use(
      http.get('/api/v1/components/:id/scan', () => new HttpResponse(null, { status: 204 })),
      http.get('/api/v1/browse/repositories/:name/oci-referrers', () =>
        HttpResponse.json({
          repository: 'docker-hosted', image: 'myapp', subject: 'sha256:abc', source: 'local',
          referrers: [
            { componentId: 'r1', reference: 'sha256:sig', digest: 'sha256:sig', artifactType: 'signature', size: 512 },
            { componentId: 'r2', reference: 'sha256:sbom', digest: 'sha256:sbom', artifactType: 'sbom', size: 2048 },
          ],
        })),
    )
    renderBrowse('?repo=docker-hosted')
    await user.click(await screen.findByText('myapp'))
    await user.click(await screen.findByText('Tags'))
    await user.click(await screen.findByText('latest'))

    expect(await screen.findByText('Referrers')).toBeInTheDocument()
    expect(await screen.findAllByTestId('referrer-row')).toHaveLength(2)
    expect(screen.getByText('sbom')).toBeInTheDocument()
    expect(screen.getByText('signature')).toBeInTheDocument()
    expect(screen.getByText('sha256:sbom')).toBeInTheDocument()
  })

  it('says an image has nothing attached rather than showing an empty list', async () => {
    const user = userEvent.setup()
    seedDocker()
    server.use(http.get('/api/v1/components/:id/scan', () => new HttpResponse(null, { status: 204 })))
    renderBrowse('?repo=docker-hosted')
    await user.click(await screen.findByText('myapp'))
    await user.click(await screen.findByText('Tags'))
    await user.click(await screen.findByText('latest'))

    expect(await screen.findByText(/No signatures, SBOMs or attestations attached/)).toBeInTheDocument()
  })

  // A proxy lists only what its cache holds; presenting that as the whole set
  // would read as "unsigned" when the truth is "not fetched".
  it('marks a proxy referrers list as cached', async () => {
    const user = userEvent.setup()
    seedDocker()
    server.use(
      http.get('/api/v1/components/:id/scan', () => new HttpResponse(null, { status: 204 })),
      http.get('/api/v1/browse/repositories/:name/oci-referrers', () =>
        HttpResponse.json({
          repository: 'docker-hosted', image: 'myapp', subject: 'sha256:abc', source: 'cache',
          referrers: [],
        })),
    )
    renderBrowse('?repo=docker-hosted')
    await user.click(await screen.findByText('myapp'))
    await user.click(await screen.findByText('Tags'))
    await user.click(await screen.findByText('latest'))

    expect(await screen.findByText('cached copies only')).toBeInTheDocument()
  })

  it('reports a failed referrers lookup instead of showing none attached', async () => {
    const user = userEvent.setup()
    seedDocker()
    server.use(
      http.get('/api/v1/components/:id/scan', () => new HttpResponse(null, { status: 204 })),
      http.get('/api/v1/browse/repositories/:name/oci-referrers', () =>
        new HttpResponse(null, { status: 500 })),
    )
    renderBrowse('?repo=docker-hosted')
    await user.click(await screen.findByText('myapp'))
    await user.click(await screen.findByText('Tags'))
    await user.click(await screen.findByText('latest'))

    expect(await screen.findByTestId('referrers-error')).toBeInTheDocument()
  })

  it('expands tree, selects a tag and shows component details', async () => {
    const user = userEvent.setup()
    seedDocker()
    server.use(http.get('/api/v1/components/:id/scan', () => new HttpResponse(null, { status: 204 })))
    renderBrowse('?repo=docker-hosted')
    await user.click(await screen.findByText('myapp'))
    await user.click(await screen.findByText('Tags'))
    await user.click(await screen.findByText('latest'))
    expect(await screen.findByText('Component details')).toBeInTheDocument()
    expect((await screen.findAllByText('docker-hosted')).length).toBeGreaterThan(0)
    // scan badge row
    expect(await screen.findByText('Vulnerability scan')).toBeInTheDocument()
  })

  it('runs a scan from the docker detail panel', async () => {
    const user = userEvent.setup()
    seedDocker()
    server.use(
      http.get('/api/v1/components/:id/scan', () => new HttpResponse(null, { status: 204 })),
      http.post('/api/v1/components/:id/scan', () =>
        HttpResponse.json({ scannedAt: new Date().toISOString(), imageRef: 'myapp:latest', status: 'ok', summary: { critical: 1, high: 0, medium: 0, low: 0, unknown: 0, total: 1 }, findings: [{ id: 'CVE-1', severity: 'CRITICAL', pkgName: 'openssl', installedVersion: '1.0', fixedVersion: '1.1', title: 'bad' }] }),
      ),
    )
    renderBrowse('?repo=docker-hosted')
    await user.click(await screen.findByText('myapp'))
    await user.click(await screen.findByText('Tags'))
    await user.click(await screen.findByText('latest'))
    await screen.findByText('Vulnerability scan')
    await user.click(screen.getByRole('button', { name: /Scan now/ }))
    expect(await screen.findByText('CRITICAL: 1')).toBeInTheDocument()
    expect(screen.getByText('openssl')).toBeInTheDocument()
  })

  // A scan can take minutes, and the detail panel is not remounted when another
  // tag is selected — so its mutation used to resolve into a render describing a
  // different component and write the finished scan into that component's cache
  // entry, which then displayed another image's vulnerabilities as its own.
  it('never writes a finished scan into the tag selected while it ran', async () => {
    const user = userEvent.setup()
    seedDocker()
    let releaseScan: (() => void) | undefined
    const scanStarted = new Promise<void>((resolve) => { releaseScan = resolve })
    const latestResult = {
      scannedAt: new Date().toISOString(), imageRef: 'myapp:latest', status: 'ok',
      summary: { critical: 1, high: 0, medium: 0, low: 0, unknown: 0, total: 1 },
      findings: [{ id: 'CVE-LATEST-ONLY', severity: 'CRITICAL', pkgName: 'openssl', installedVersion: '1.0', fixedVersion: '1.1', title: 'bad' }],
    }
    let latestScanned = false
    server.use(
      // Like the real endpoint: a result exists only for a component that has
      // actually been scanned.
      http.get('/api/v1/components/:id/scan', ({ params }) =>
        params.id === 'dc1' && latestScanned
          ? HttpResponse.json(latestResult)
          : new HttpResponse(null, { status: 204 })),
      http.post('/api/v1/components/:id/scan', async () => {
        // Held open until the test has switched tags, standing in for the real
        // multi-minute scan.
        await scanStarted
        latestScanned = true
        return HttpResponse.json(latestResult)
      }),
    )

    // The shared test client sets gcTime: 0, which drops a component's detail
    // the moment it loses its observer — so every tag switch would remount the
    // panel and, with it, tear down the in-flight scan. Real users have the
    // default cache, where the panel survives the switch. That survival is the
    // whole precondition for this bug, so this test keeps a real cache.
    const qc = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    })
    renderBrowse('?repo=docker-hosted', qc)
    await user.click(await screen.findByText('myapp'))
    await user.click(await screen.findByText('Tags'))

    // Visit both tags first so their details are cached: that is what keeps the
    // detail panel — and the scan row inside it — mounted across the switch
    // below, instead of remounting through a loading state.
    await user.click(await screen.findByText('stable'))
    await screen.findByText('Vulnerability scan')
    await user.click(screen.getByText('latest'))
    await screen.findByText('Vulnerability scan')

    await user.click(screen.getByRole('button', { name: /Scan now/ }))

    // Switch to the other tag while the scan is still running.
    await user.click(screen.getByText('stable'))
    expect(await screen.findByText('Not scanned yet')).toBeInTheDocument()

    releaseScan?.()

    // The result lands on the tag it was started for — which also waits for the
    // scan to have settled.
    await user.click(screen.getByText('latest'))
    expect(await screen.findByText('CRITICAL: 1')).toBeInTheDocument()

    // The scan landed in the cache entry of the component it was started for,
    // and nothing was written into the one selected while it ran.
    expect(qc.getQueryData(['scanResult', 'dc1'])).toBeTruthy()
    expect(qc.getQueryData(['scanResult', 'dc2'])).toBeFalsy()

    // And 'stable' still shows its own state, not the finished scan of 'latest'.
    await user.click(screen.getByText('stable'))
    expect(await screen.findByText('Not scanned yet')).toBeInTheDocument()
    expect(screen.queryByText('CVE-LATEST-ONLY')).not.toBeInTheDocument()
    expect(screen.queryByText('CRITICAL: 1')).not.toBeInTheDocument()
  })

  // A malicious-package report has no CVSS severity. Without a badge of its own
  // it was counted as UNKNOWN — indistinguishable from "the scanner could not
  // classify this".
  it('shows a MALICIOUS badge and filter for a malicious-package finding', async () => {
    const user = userEvent.setup()
    seedDocker()
    server.use(
      http.get('/api/v1/components/:id/scan', () => new HttpResponse(null, { status: 204 })),
      http.post('/api/v1/components/:id/scan', () =>
        HttpResponse.json({
          scannedAt: new Date().toISOString(), imageRef: 'debug:4.4.2', status: 'ok',
          summary: { malicious: 1, critical: 0, high: 0, medium: 0, low: 0, unknown: 0, total: 1 },
          findings: [{ id: 'MAL-2025-46974', severity: 'MALICIOUS', pkgName: 'debug', installedVersion: '4.4.2', title: 'Malicious code in debug' }],
        }),
      ),
    )
    renderBrowse('?repo=docker-hosted')
    await user.click(await screen.findByText('myapp'))
    await user.click(await screen.findByText('Tags'))
    await user.click(await screen.findByText('latest'))
    await screen.findByText('Vulnerability scan')
    await user.click(screen.getByRole('button', { name: /Scan now/ }))

    expect(await screen.findByText('MALICIOUS: 1')).toBeInTheDocument()
    expect(screen.getByText('MAL-2025-46974')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'MALICIOUS (1)' })).toBeInTheDocument()
  })

  it('opens Example Usage and Promote from docker panel', async () => {
    const user = userEvent.setup()
    seedDocker()
    server.use(
      http.get('/api/v1/components/:id/scan', () => new HttpResponse(null, { status: 204 })),
      http.get('/api/v1/components/:id/promotion-rules', () =>
        HttpResponse.json([{ id: 'pr1', name: 'rel', from_repo: 'docker-hosted', to_repo: 'docker-release', require_scan_pass: false, require_manual_approval: false }]),
      ),
    )
    renderBrowse('?repo=docker-hosted')
    await user.click(await screen.findByText('myapp'))
    await user.click(await screen.findByText('Tags'))
    await user.click(await screen.findByText('latest'))
    await screen.findByText('Component details')
    await user.click(screen.getByRole('button', { name: /Example Usage/ }))
    expect(await screen.findByText('Documentation coming soon')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Close' }))
    await user.click(screen.getByRole('button', { name: /Promote/ }))
    expect(await screen.findByText(/Promote 1 component/)).toBeInTheDocument()
  })

  it('promote with no rules alerts', async () => {
    const user = userEvent.setup()
    seedDocker()
    const alertSpy = vi.spyOn(window, 'alert').mockImplementation(() => {})
    server.use(
      http.get('/api/v1/components/:id/scan', () => new HttpResponse(null, { status: 204 })),
      http.get('/api/v1/components/:id/promotion-rules', () => HttpResponse.json([])),
    )
    renderBrowse('?repo=docker-hosted')
    await user.click(await screen.findByText('myapp'))
    await user.click(await screen.findByText('Tags'))
    await user.click(await screen.findByText('latest'))
    await screen.findByText('Component details')
    await user.click(screen.getByRole('button', { name: /Promote/ }))
    await waitFor(() => expect(alertSpy).toHaveBeenCalled())
  })

  it('deletes a docker tag', async () => {
    const user = userEvent.setup()
    let deleted = false
    seedDocker()
    server.use(
      http.delete('/api/v1/browse/repositories/:name/docker-tag', () => { deleted = true; return new HttpResponse(null, { status: 204 }) }),
    )
    renderBrowse('?repo=docker-hosted')
    await user.click(await screen.findByText('myapp'))
    await user.click(await screen.findByText('Tags'))
    const tagRow = (await screen.findByText('latest')).closest('[role="button"]')!
    fireEvent.click(within(tagRow as HTMLElement).getByTitle('Delete tag'))
    expect(await screen.findByText('Delete file?')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: /^Delete$/ }))
    await waitFor(() => expect(deleted).toBe(true))
  })

  it('refreshes the docker tree', async () => {
    const user = userEvent.setup()
    let calls = 0
    server.use(
      http.get('/api/v1/browse/repositories/:name/docker-tree', () => { calls++; return HttpResponse.json(dockerTree) }),
    )
    renderBrowse('?repo=docker-hosted')
    await screen.findByText('myapp')
    const before = calls
    await user.click(screen.getByRole('button', { name: 'Refresh' }))
    await waitFor(() => expect(calls).toBeGreaterThan(before))
  })

  it('auto-drills to a component via ?cid= URL param', async () => {
    seedDocker()
    server.use(http.get('/api/v1/components/:id/scan', () => new HttpResponse(null, { status: 204 })))
    Element.prototype.scrollIntoView = vi.fn()
    renderBrowse('?repo=docker-hosted&cid=dc1')
    expect(await screen.findByText('Component details')).toBeInTheDocument()
    expect(await screen.findByText('Vulnerability scan')).toBeInTheDocument()
  })
})

describe('BrowsePage — OCI tree', () => {
  it('renders the registry tree (not the generic component list) for an oci repository', async () => {
    server.use(
      http.get('/api/v1/browse/repositories/:name/docker-tree', () =>
        HttpResponse.json({
          root: {
            kind: 'folder', label: '', path: '', children: [
              {
                kind: 'folder', label: 'my-chart', path: '/my-chart', imageRef: 'my-chart', children: [
                  {
                    kind: 'folder', label: 'Tags', path: '/my-chart/Tags', children: [
                      { kind: 'tag', label: '1.0.0', path: '/my-chart/Tags/1.0.0', imageRef: 'my-chart', version: '1.0.0', componentId: 'oc1' },
                    ],
                  },
                ],
              },
            ],
          },
        }),
      ),
    )
    renderBrowse('?repo=oci-hosted')
    expect(await screen.findByText('my-chart')).toBeInTheDocument()
    expect(screen.queryByText('No components in this repository')).not.toBeInTheDocument()
  })

  // The tree is where a chart, a WASM module and a signature stop looking alike.
  function seedOciTree(leaves: Record<string, unknown>[]) {
    server.use(
      http.get('/api/v1/browse/repositories/:name/docker-tree', () =>
        HttpResponse.json({
          root: {
            kind: 'folder', label: '', path: '', children: [
              {
                kind: 'folder', label: 'my-chart', path: '/my-chart', imageRef: 'my-chart', children: [
                  { kind: 'folder', label: 'Tags', path: '/my-chart/Tags', children: leaves },
                ],
              },
            ],
          },
        }),
      ),
    )
  }

  it('badges a tag with the artifact type the manifest declared', async () => {
    const user = userEvent.setup()
    seedOciTree([
      { kind: 'tag', label: '1.0.0', path: '/my-chart/Tags/1.0.0', imageRef: 'my-chart', version: '1.0.0', componentId: 'oc1', artifactType: 'chart' },
    ])
    renderBrowse('?repo=oci-hosted')
    await user.click(await screen.findByText('my-chart'))
    await user.click(await screen.findByText('Tags'))
    const tagRow = (await screen.findByText('1.0.0')).closest('[role="button"]') as HTMLElement
    expect(within(tagRow).getByText('chart')).toBeInTheDocument()
  })

  it('shows an unrecognized artifact type verbatim', async () => {
    const user = userEvent.setup()
    seedOciTree([
      { kind: 'tag', label: '2.0.0', path: '/my-chart/Tags/2.0.0', imageRef: 'my-chart', version: '2.0.0', componentId: 'oc2', artifactType: 'application/vnd.acme.model.config.v1+json' },
    ])
    renderBrowse('?repo=oci-hosted')
    await user.click(await screen.findByText('my-chart'))
    await user.click(await screen.findByText('Tags'))
    const tagRow = (await screen.findByText('2.0.0')).closest('[role="button"]') as HTMLElement
    expect(within(tagRow).getByText('application/vnd.acme.model.config.v1+json')).toBeInTheDocument()
  })

  // Everything pushed before this feature existed arrives without the field, and
  // must render exactly as it did then — no badge, empty or otherwise.
  it('renders no badge for a tag with no artifact type', async () => {
    const user = userEvent.setup()
    seedOciTree([
      { kind: 'tag', label: '3.0.0', path: '/my-chart/Tags/3.0.0', imageRef: 'my-chart', version: '3.0.0', componentId: 'oc3' },
    ])
    renderBrowse('?repo=oci-hosted')
    await user.click(await screen.findByText('my-chart'))
    await user.click(await screen.findByText('Tags'))
    const tagRow = (await screen.findByText('3.0.0')).closest('[role="button"]') as HTMLElement
    expect(within(tagRow).queryByTestId('artifact-type')).not.toBeInTheDocument()
  })
})

describe('BrowsePage — promote flow', () => {
  it('promotes selected components successfully', async () => {
    const user = userEvent.setup()
    server.use(
      http.get('/service/rest/v1/components', () =>
        HttpResponse.json({ items: [{ id: 'c1', name: 'pkg-a', group: '', version: '1', format: 'maven2', assets: [] }], continuationToken: null }),
      ),
      http.get('/api/v1/components/:id/promotion-rules', () =>
        HttpResponse.json([{ id: 'pr1', name: 'rel', from_repo: 'maven-hosted', to_repo: 'maven-release', require_scan_pass: false, require_manual_approval: false }]),
      ),
      http.post('/api/v1/promotion/promote', () => HttpResponse.json({ requests: [{ status: 'completed' }] })),
    )
    renderBrowse('?repo=maven-hosted')
    await screen.findByText('pkg-a')
    await user.click(screen.getAllByRole('checkbox')[0])
    await user.click(await screen.findByRole('button', { name: /Promote selected/ }))
    await screen.findByText(/Promote 1 component/)
    await user.click(screen.getByRole('button', { name: /Select a rule/ }))
    await user.click(await screen.findByText(/rel \(/))
    const promoteBtns = screen.getAllByRole('button', { name: /^Promote$/ })
    await user.click(promoteBtns[promoteBtns.length - 1])
    expect(await screen.findByText(/Promoted 1 component/)).toBeInTheDocument()
  })

  it('shows promotion error', async () => {
    const user = userEvent.setup()
    server.use(
      http.get('/service/rest/v1/components', () =>
        HttpResponse.json({ items: [{ id: 'c1', name: 'pkg-a', group: '', version: '1', format: 'maven2', assets: [] }], continuationToken: null }),
      ),
      http.get('/api/v1/components/:id/promotion-rules', () =>
        HttpResponse.json([{ id: 'pr1', name: 'rel', from_repo: 'maven-hosted', to_repo: 'maven-release', require_scan_pass: false, require_manual_approval: true }]),
      ),
      http.post('/api/v1/promotion/promote', () => HttpResponse.json({ error: 'promote failed' }, { status: 400 })),
    )
    renderBrowse('?repo=maven-hosted')
    await screen.findByText('pkg-a')
    await user.click(screen.getAllByRole('checkbox')[0])
    await user.click(await screen.findByRole('button', { name: /Promote selected/ }))
    await screen.findByText(/Promote 1 component/)
    await user.click(screen.getByRole('button', { name: /Select a rule/ }))
    await user.click(await screen.findByText(/rel \(/))
    const promoteBtns = screen.getAllByRole('button', { name: /^Promote$/ })
    await user.click(promoteBtns[promoteBtns.length - 1])
    expect(await screen.findByText(/Error: promote failed/)).toBeInTheDocument()
  })
})

describe('BrowsePage — non-admin', () => {
  it('hides delete actions and upload for non-admin without privileges', async () => {
    useAuthStore.setState({ token: 'tok', user: fixtures.user({ roles: ['viewer'] }) as ReturnType<typeof fixtures.user> })
    server.use(
      http.get('/api/v1/me/privileges', () => HttpResponse.json([])),
      http.get('/service/rest/v1/components', () =>
        HttpResponse.json({ items: [{ id: 'c1', name: 'pkg-a', group: '', version: '1', format: 'maven2', assets: [{ id: 'a1', path: 'p.jar', fileSize: 1, contentType: 't' }] }], continuationToken: null }),
      ),
    )
    renderBrowse('?repo=maven-hosted')
    await screen.findByText('pkg-a')
    expect(screen.queryByTitle('Delete')).not.toBeInTheDocument()
  })
})

// A visitor with no session may browse public repositories (#404). The
// privilege lookup, promotion and scan results are signed-in surfaces: they
// are neither requested nor offered, so the page renders without a single
// 401 — the response interceptor of the time turned the first one into a
// redirect to /login.
describe('BrowsePage — signed-out visitor', () => {
  beforeEach(() => {
    seedAuthAsGuest()
    window.location.href = 'http://localhost/'
  })

  it('browses a public repository without privileges, selection or promote', async () => {
    const user = userEvent.setup()
    let privilegeHits = 0
    server.use(
      http.get('/api/v1/me/privileges', () => {
        privilegeHits++
        return HttpResponse.json({ error: 'authentication required' }, { status: 401 })
      }),
      http.get('/service/rest/v1/components', () =>
        HttpResponse.json({
          items: [
            { id: 'c1', name: 'pkg-a', group: 'com.example', version: '1.0', format: 'maven2', assets: [{ id: 'a1', path: 'com/example/pkg-a/1.0/pkg-a.jar', fileSize: 2048, contentType: 'application/java-archive' }] },
          ],
          continuationToken: null,
        }),
      ),
    )
    renderBrowse()
    await screen.findByText('Choose a repository above')
    await user.click(screen.getByRole('button', { name: /Select repository/ }))
    await user.click((await screen.findAllByText('maven-hosted'))[0])
    expect(await screen.findByText('pkg-a')).toBeInTheDocument()
    await new Promise((r) => setTimeout(r, 50))
    expect(privilegeHits).toBe(0)
    expect(screen.queryByRole('checkbox')).not.toBeInTheDocument()
    expect(screen.queryByText(/Promote/)).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Upload/ })).not.toBeInTheDocument()
    expect(window.location.href).toBe('http://localhost/')
  })
})
