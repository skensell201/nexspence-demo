import axios from 'axios'

// Shape of an axios-style error as consumed throughout the UI: the backend puts a
// human-readable message in `response.data.error`; `message` is axios's own fallback.
export interface ApiError {
  response?: { status?: number; data?: { error?: string } }
  message?: string
}

// Extract a user-facing message from an unknown caught error, with a fallback.
export function apiErrorMessage(e: unknown, fallback: string): string {
  const err = e as ApiError
  return err?.response?.data?.error ?? err?.message ?? fallback
}

export const apiClient = axios.create({
  baseURL: import.meta.env.VITE_API_BASE || '',
  headers: { 'Content-Type': 'application/json' },
})

// Attach JWT token from localStorage.
// Also strip the default application/json Content-Type when sending FormData so
// the browser fills in the correct multipart/form-data boundary automatically.
apiClient.interceptors.request.use((config) => {
  const token = localStorage.getItem('nexspence_token')
  if (token) {
    config.headers.Authorization = `Bearer ${token}`
  }
  if (config.data instanceof FormData) {
    delete (config.headers as Record<string, unknown>)['Content-Type']
  }
  return config
})

// A 401 means "sign in" only when the request carried a token that the server
// refused — the session expired or was revoked — so clear it and go to /login.
// A signed-out visitor browsing public repositories (#404) has no session to
// lose: a 401 there is just a resource they cannot read, and bouncing them to
// the login wall would undo the anonymous UI. NOT when the 401 comes from the
// login endpoint itself (otherwise the error never reaches the form and
// "nothing happens").
apiClient.interceptors.response.use(
  (r) => r,
  (err) => {
    const url: string = err.config?.url ?? ''
    const presentedToken = Boolean(err.config?.headers?.Authorization)
    if (err.response?.status === 401 && presentedToken && !url.endsWith('/login')) {
      localStorage.removeItem('nexspence_token')
      window.location.href = '/login'
    }
    return Promise.reject(err)
  },
)

// ── Domain types ─────────────────────────────────────────────

export interface AuthConfig {
  oidcEnabled: boolean
  oidcDisplayName: string
  oidcLoginUrl: string
  ldapEnabled: boolean
  samlEnabled?: boolean
  samlDisplayName?: string
  samlLoginUrl?: string
  samlEntityId?: string
  samlAcsUrl?: string
  samlIdpMetadataUrl?: string
  samlProvisioning?: string
  samlMetadataUrl?: string
}

export interface ServiceStatus {
  name: string
  status: 'ok' | 'warn' | 'error' | 'disabled'
  latency_ms?: number
  detail: string
  checked_at: string
}

export interface Privilege {
  id: string
  type?: string
  contentSelectorId?: string
  attrs?: { actions?: string[] }
}

export interface CleanupPreviewAsset {
  path: string
  repository: string
  sizeBytes: number
  lastDownloaded: string | null
  createdAt: string
  reason: string
}

export interface CleanupPreviewResponse {
  assets: CleanupPreviewAsset[]
  totalCount: number
  totalBytes: number
}

export interface ImportRepoStats {
  repository: string
  components: number
  assets: number
  blobs: number
  conflictMode: string
}

export interface BlobStoreMigration {
  id: string;
  repositoryName: string;
  sourceStoreId: string;
  targetStoreId: string;
  status: 'pending' | 'running' | 'cancelled' | 'done' | 'failed';
  totalAssets: number;
  doneAssets: number;
  totalBytes: number;
  doneBytes: number;
  errorMessage: string | null;
  startedAt: string | null;
  finishedAt: string | null;
  createdAt: string;
  updatedAt: string;
}

export interface RoutingRule {
  id: string
  name: string
  description?: string
  mode: 'ALLOW' | 'BLOCK'
  matchers: string[]
  createdAt: string
  updatedAt: string
}

export type RoutingRuleInput = Omit<RoutingRule, 'id' | 'createdAt' | 'updatedAt'>

export interface ReplicationRule {
  id: string
  name: string
  source_repo: string
  target_url: string
  target_repo: string
  target_username: string
  cron_expr: string
  enabled: boolean
  last_run_at: string | null
  last_run_status: string
  created_at: string
}

export interface ReplicationHistory {
  id: string
  rule_id: string
  started_at: string
  finished_at: string | null
  duration_ms: number
  pushed_count: number
  skipped_count: number
  failed_count: number
  transferred_bytes: number
  error: string
}

// One artifact that names an image manifest as its subject: a signature, an
// SBOM, an attestation. `artifactType` is the friendly label the browse tree
// uses, `rawType` the media type it was derived from.
export interface OCIReferrer {
  componentId: string
  reference: string
  digest?: string
  artifactType?: string
  rawType?: string
  mediaType?: string
  size?: number
}

export interface OCIReferrersResponse {
  repository: string
  image: string
  subject: string
  // 'cache' for a proxy repository, whose list is only what was pulled through
  // it — an empty list there means "not fetched", never "not signed".
  source: 'local' | 'cache'
  referrers: OCIReferrer[]
}

export interface ScannerStatus {
  state: 'disabled' | 'missing' | 'broken' | 'ready'
  version?: string
  path?: string
  message: string
}

export interface ReplicationRuleInput {
  name: string
  source_repo: string
  target_url: string
  target_repo: string
  target_username: string
  target_password: string
  cron_expr: string
  enabled: boolean
}

// ── API helpers ──────────────────────────────────────────────

export const nexusApi = {
  // Auth
  login: (username: string, password: string) =>
    apiClient.post('/api/v1/login', { username, password }),
  me: () => apiClient.get('/api/v1/me'),
  getAuthConfig: async (): Promise<AuthConfig> => {
    const { data } = await apiClient.get<AuthConfig>('/api/v1/auth/config')
    return data
  },

  // Repositories
  listRepositories: (params?: { format?: string; type?: string }) =>
    apiClient.get('/service/rest/v1/repositories', { params }),
  getRepository: (name: string) =>
    apiClient.get(`/service/rest/v1/repositories/${name}`),
  createRepository: (format: string, type: string, data: unknown) =>
    apiClient.post(`/service/rest/v1/repositories/${format}/${type}`, data),
  deleteRepository: (name: string) =>
    apiClient.delete(`/service/rest/v1/repositories/${name}`),
  updateRepository: (
    format: string,
    type: string,
    name: string,
    data: Record<string, unknown>,
  ) =>
    apiClient.put(`/service/rest/v1/repositories/${format}/${type}/${name}`, data),
  patchRepository: (name: string, patch: { online: boolean }) =>
    apiClient.patch(`/service/rest/v1/repositories/${name}`, patch),

  // Components
  listComponents: (repository: string, continuationToken?: string) =>
    apiClient.get('/service/rest/v1/components', {
      params: { repository, continuationToken },
    }),
  getComponent: (id: string) => apiClient.get(`/service/rest/v1/components/${id}`),
  deleteComponent: (id: string) =>
    apiClient.delete(`/service/rest/v1/components/${id}`),
  setComponentTags: (id: string, tags: string[]) =>
    apiClient.put<{ tags: string[] }>(`/service/rest/v1/components/${encodeURIComponent(id)}/tags`, { tags }),

  // Search
  search: (params: Record<string, string | undefined>) =>
    apiClient.get('/service/rest/v1/search', { params }),

  // Users
  listUsers: () => apiClient.get('/service/rest/v1/security/users'),
  getUser: (userId: string) =>
    apiClient.get(`/service/rest/v1/security/users/${userId}`),
  createUser: (data: unknown) =>
    apiClient.post('/service/rest/v1/security/users', data),
  updateUser: (userId: string, data: unknown) =>
    apiClient.put(`/service/rest/v1/security/users/${userId}`, data),
  deleteUser: (userId: string) =>
    apiClient.delete(`/service/rest/v1/security/users/${userId}`),
  changePassword: (userId: string, password: string) =>
    apiClient.put(
      `/service/rest/v1/security/users/${userId}/change-password`,
      password,
      { headers: { 'Content-Type': 'text/plain' } },
    ),

  // Roles
  listRoles: () => apiClient.get('/service/rest/v1/security/roles'),
  createRole: (data: { name: string; description?: string }) =>
    apiClient.post('/service/rest/v1/security/roles', data),
  deleteRole: (id: string) =>
    apiClient.delete(`/service/rest/v1/security/roles/${id}`),
  setUserRoles: (userId: string, roleIds: string[]) =>
    apiClient.put(`/service/rest/v1/security/users/${userId}/roles`, { roleIds }),

  // Role privileges
  listRolePrivileges: (roleId: string) =>
    apiClient.get(`/service/rest/v1/security/roles/${roleId}/privileges`),
  setRolePrivileges: (roleId: string, privilegeIds: string[]) =>
    apiClient.put(`/service/rest/v1/security/roles/${roleId}/privileges`, { privilegeIds }),
  updateRole: (id: string, data: { name: string; description?: string }) =>
    apiClient.put(`/service/rest/v1/security/roles/${id}`, data),

  // Privileges
  listPrivileges: () =>
    apiClient.get('/service/rest/v1/security/privileges'),
  createPrivilege: (data: unknown) =>
    apiClient.post('/service/rest/v1/security/privileges', data),
  updatePrivilege: (id: string, data: unknown) =>
    apiClient.put(`/service/rest/v1/security/privileges/${id}`, data),
  deletePrivilege: (id: string) =>
    apiClient.delete(`/service/rest/v1/security/privileges/${id}`),

  // Content selectors
  listContentSelectors: () =>
    apiClient.get('/service/rest/v1/security/content-selectors'),
  createContentSelector: (data: unknown) =>
    apiClient.post('/service/rest/v1/security/content-selectors', data),
  updateContentSelector: (id: string, data: unknown) =>
    apiClient.put(`/service/rest/v1/security/content-selectors/${id}`, data),
  deleteContentSelector: (id: string) =>
    apiClient.delete(`/service/rest/v1/security/content-selectors/${id}`),
  attachContentSelector: (privilegeName: string, selectorId: string) =>
    apiClient.put(`/service/rest/v1/security/privileges/${privilegeName}/content-selector/${selectorId}`),
  detachContentSelector: (privilegeName: string) =>
    apiClient.delete(`/service/rest/v1/security/privileges/${privilegeName}/content-selector`),

  // Blob stores
  listBlobStores: () => apiClient.get('/service/rest/v1/blobstores'),
  createBlobStore: (type: string, data: unknown) =>
    apiClient.post(`/service/rest/v1/blobstores/${type}`, data),
  updateBlobStore: (type: string, name: string, data: unknown) =>
    apiClient.put(`/service/rest/v1/blobstores/${type}/${name}`, data),
  deleteBlobStore: (name: string) =>
    apiClient.delete(`/service/rest/v1/blobstores/${name}`),
  getBlobStoreUsage: (name: string) =>
    apiClient.get(`/api/v1/blob-stores/${name}/usage`),
  compactBlobStore: (name: string, opts?: { dryRun?: boolean; minAge?: string }) =>
    apiClient.post(`/api/v1/blobstores/${name}/compact`, null, {
      params: {
        dry_run: opts?.dryRun ? 'true' : undefined,
        min_age: opts?.minAge || undefined,
      },
    }),
  testBlobStore: (type: string, config: Record<string, unknown>) =>
    apiClient.post<{ ok: boolean; error?: string }>('/api/v1/blobstores/test', { type, config }),

  // Cleanup policies
  listCleanupPolicies: () => apiClient.get('/service/rest/v1/cleanup-policies'),
  getCleanupPolicy: (id: string) =>
    apiClient.get(`/service/rest/v1/cleanup-policies/${id}`),
  createCleanupPolicy: (data: unknown) =>
    apiClient.post('/service/rest/v1/cleanup-policies', data),
  updateCleanupPolicy: (id: string, data: unknown) =>
    apiClient.put(`/service/rest/v1/cleanup-policies/${id}`, data),
  deleteCleanupPolicy: (id: string) =>
    apiClient.delete(`/service/rest/v1/cleanup-policies/${id}`),
  runCleanupPolicy: (id: string) =>
    apiClient.post(`/service/rest/v1/cleanup-policies/${id}/run`),
  previewCleanupPolicy: (id: string) =>
    apiClient.post<CleanupPreviewResponse>(`/api/v1/cleanup-policies/${id}/preview`),

  // Routing rules
  listRoutingRules: () =>
    apiClient.get<RoutingRule[]>('/service/rest/v1/routing-rules'),
  createRoutingRule: (data: RoutingRuleInput) =>
    apiClient.post<RoutingRule>('/service/rest/v1/routing-rules', data),
  updateRoutingRule: (id: string, data: RoutingRuleInput) =>
    apiClient.put<RoutingRule>(`/service/rest/v1/routing-rules/${id}`, data),
  deleteRoutingRule: (id: string) =>
    apiClient.delete(`/service/rest/v1/routing-rules/${id}`),

  // Audit log
  listAuditEvents: (params?: {
    domain?: string
    action?: string
    username?: string
    from?: string   // YYYY-MM-DD or RFC3339
    to?: string     // YYYY-MM-DD or RFC3339
    limit?: number
    offset?: number
  }) => apiClient.get('/service/rest/v1/audit', { params }),

  // Returns a URL that triggers a NDJSON download in the browser. Accepts the
  // same filter shape as `listAuditEvents` minus pagination.
  auditExportUrl: (params?: {
    domain?: string
    action?: string
    username?: string
    from?: string
    to?: string
  }): string => {
    const sp = new URLSearchParams({ format: 'ndjson' })
    if (params?.domain)   sp.set('domain', params.domain)
    if (params?.action)   sp.set('action', params.action)
    if (params?.username) sp.set('username', params.username)
    if (params?.from)     sp.set('from', params.from)
    if (params?.to)       sp.set('to', params.to)
    const base = (apiClient.defaults.baseURL ?? '').replace(/\/$/, '')
    return `${base}/service/rest/v1/audit?${sp.toString()}`
  },

  // Metrics
  getMetrics: () => apiClient.get('/api/v1/metrics'),

  // System status
  getStatus: () => apiClient.get('/service/rest/v1/status'),

  // Browse — path tree for content selector dropdowns
  listPathTree: (repoName: string, q?: string) =>
    apiClient.get<{ paths: string[] }>(
      `/api/v1/browse/repositories/${encodeURIComponent(repoName)}/path-tree`,
      { params: q ? { q } : {} },
    ),
}

export const nexspenceApi = {
  // Migration (Nexspence-native)
  listMigrationJobs: () => apiClient.get('/api/v1/migration/jobs'),
  createMigrationJob: (data: unknown) =>
    apiClient.post('/api/v1/migration/jobs', data),
  getMigrationJob: (id: string) =>
    apiClient.get(`/api/v1/migration/jobs/${id}`),
  pauseMigrationJob: (id: string) =>
    apiClient.post(`/api/v1/migration/jobs/${id}/pause`),
  resumeMigrationJob: (id: string) =>
    apiClient.post(`/api/v1/migration/jobs/${id}/resume`),
  previewMigration: (data: unknown) =>
    apiClient.post('/api/v1/migration/preview', data),

  // System info + service health
  getSystemInfo: () => apiClient.get('/api/v1/system/info'),
  getServiceStatuses: () => apiClient.get<ServiceStatus[]>('/api/v1/system/services'),

  // Backup / restore (admin) — see handlers/backup.go
  exportBackup: () =>
    apiClient.get('/api/v1/backup/export', { responseType: 'blob' }),
  restoreBackup: (file: File) => {
    const fd = new FormData()
    fd.append('file', file)
    return apiClient.post<{ restored: Record<string, number> }>('/api/v1/backup/restore', fd)
  },

  exportRepo: (name: string) =>
    apiClient.get(`/api/v1/repositories/${encodeURIComponent(name)}/export`, { responseType: 'blob' }),

  importRepo: (file: File, targetName: string, conflictMode: string) => {
    const fd = new FormData()
    fd.append('file', file)
    fd.append('targetName', targetName)
    fd.append('conflictMode', conflictMode)
    return apiClient.post<{ imported: ImportRepoStats }>('/api/v1/repositories/import', fd)
  },

  // Repository storage usage vs quota — GET /api/v1/repositories/:name/quota
  getRepositoryQuota: (repoName: string) =>
    apiClient.get<{
      usedBytes: number
      quotaBytes: number | null
      percentUsed: number | null
    }>(`/api/v1/repositories/${encodeURIComponent(repoName)}/quota`),

  // Browse — Nexus-style Docker tree (image / Tags | Manifests | Blobs)
  getDockerBrowseTree: (repository: string) =>
    apiClient.get(`/api/v1/browse/repositories/${encodeURIComponent(repository)}/docker-tree`),

  // Browse — what refers to an image manifest (signatures, SBOMs, attestations).
  // A referrer only points at its subject, never the other way round, so this is
  // the only way the UI can show them grouped under the image they describe.
  getOCIReferrers: (repository: string, image: string, digest: string) =>
    apiClient.get<OCIReferrersResponse>(
      `/api/v1/browse/repositories/${encodeURIComponent(repository)}/oci-referrers`,
      { params: { image, digest } },
    ),

  // Browse — Raw file tree (path hierarchy from assets)
  getRawBrowseTree: (repository: string) =>
    apiClient.get(`/api/v1/browse/repositories/${encodeURIComponent(repository)}/raw-tree`),

  // Security — privilege → role membership map
  privilegeRoleMap: () =>
    apiClient.get<Record<string, string[]>>('/api/v1/security/privilege-role-map').then(r => r.data),

  // Current user's effective privileges
  myPrivileges: () =>
    apiClient.get<Privilege[]>('/api/v1/me/privileges').then(r => r.data),

  // Delete artifact by path (non-docker)
  deleteByPath: (repoName: string, path: string) =>
    apiClient.delete(`/api/v1/browse/repositories/${encodeURIComponent(repoName)}/path`, {
      params: { path },
    }),

  // Delete Docker tag: cascades manifest + digest alias + unreferenced blobs
  deleteDockerTag: (repoName: string, image: string, ref: string) =>
    apiClient.delete(`/api/v1/browse/repositories/${encodeURIComponent(repoName)}/docker-tag`, {
      params: { image, ref },
    }),

  // Delete all tags/manifests/blobs for a Docker image or namespace prefix
  deleteDockerImage: (repoName: string, image: string) =>
    apiClient.delete(`/api/v1/browse/repositories/${encodeURIComponent(repoName)}/docker-image`, {
      params: { image },
    }),

  // Vulnerability scan (Trivy) — Docker components
  // GET returns 204 No Content when no cached scan yet (not an error); we map that to data: null.
  getScanResult: (componentId: string) =>
    apiClient
      .get(`/api/v1/components/${encodeURIComponent(componentId)}/scan`)
      .then((r) => (r.status === 204 ? { ...r, data: null } : r)),
  scanComponent: (componentId: string, body?: { imageRef?: string }) =>
    apiClient.post(
      `/api/v1/components/${encodeURIComponent(componentId)}/scan`,
      body ?? {},
    ),

  // Whether image scanning is available at all. Nexspence ships no scanner:
  // the binary is supplied by the operator, so the UI has to ask.
  getScannerStatus: () => apiClient.get<ScannerStatus>('/api/v1/security/scanner'),

  // Scan-result exports — same endpoints, ?export= switches them to a file
  // download of the whole filtered set rather than a page.
  exportVulnerabilities: (
    filters: { repo?: string; severity?: string; format?: string },
    as: 'csv' | 'json',
  ) =>
    apiClient.get(`/api/v1/security/vulnerabilities`, {
      params: { ...filters, export: as },
      responseType: 'blob',
    }),
  exportScanResult: (componentId: string, as: 'csv' | 'json') =>
    apiClient.get(`/api/v1/components/${encodeURIComponent(componentId)}/scan`, {
      params: { export: as },
      responseType: 'blob',
    }),

  // Replication rules
  listReplicationRules: () =>
    apiClient.get<ReplicationRule[]>('/api/v1/replication/rules'),
  createReplicationRule: (data: ReplicationRuleInput) =>
    apiClient.post<ReplicationRule>('/api/v1/replication/rules', data),
  updateReplicationRule: (id: string, data: ReplicationRuleInput) =>
    apiClient.put<ReplicationRule>(`/api/v1/replication/rules/${id}`, data),
  deleteReplicationRule: (id: string) =>
    apiClient.delete(`/api/v1/replication/rules/${id}`),
  runReplicationRule: (id: string) =>
    apiClient.post(`/api/v1/replication/rules/${id}/run`),
  testReplicationRule: (id: string) =>
    apiClient.post<{ ok: boolean }>(`/api/v1/replication/rules/${id}/test`),
  listReplicationHistory: (id: string) =>
    apiClient.get<ReplicationHistory[]>(`/api/v1/replication/rules/${id}/history`),
}

export async function startBlobStoreMigration(
  repoName: string,
  targetStoreId: string,
): Promise<BlobStoreMigration> {
  const { data } = await apiClient.post<BlobStoreMigration>(
    `/api/v1/repositories/${encodeURIComponent(repoName)}/migrate-blob-store`,
    { targetStoreId },
  )
  return data
}

export async function getBlobStoreMigration(
  repoName: string,
): Promise<BlobStoreMigration | null> {
  try {
    const { data } = await apiClient.get<BlobStoreMigration>(
      `/api/v1/repositories/${encodeURIComponent(repoName)}/blob-store-migration`,
    )
    return data
  } catch (err) {
    if ((err as ApiError)?.response?.status === 404) return null
    throw err
  }
}

export async function cancelBlobStoreMigration(repoName: string): Promise<void> {
  await apiClient.delete(
    `/api/v1/repositories/${encodeURIComponent(repoName)}/blob-store-migration`,
  )
}
