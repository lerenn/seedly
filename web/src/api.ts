export type Role = 'admin' | 'user'

export type User = {
  id: number
  username: string
  display_name: string
  role: Role
  created_at: string
}

export type TorrentStatus = 'downloading' | 'seeding' | 'paused' | 'error'

export type LiveStats = {
  progress: number
  downloaded: number
  uploaded: number
  download_rate: number
  upload_rate: number
  peers: number
  total_length: number
  bytes_completed: number
  complete: boolean
  file_count: number
}

export type TorrentView = {
  id: number
  owner_id: number
  info_hash: string
  name: string
  status: TorrentStatus
  error_message?: string
  created_at: string
  completed_at?: string
  stats: LiveStats
}

export type DiskUsage = {
  path: string
  total_bytes: number
  free_bytes: number
  seedly_bytes: number
  other_bytes: number
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    credentials: 'include',
    ...init,
    headers: {
      ...(init?.body instanceof FormData ? {} : { 'Content-Type': 'application/json' }),
      ...init?.headers,
    },
  })
  if (!res.ok) {
    let msg = res.statusText
    try {
      const body = await res.json()
      if (body?.error) msg = body.error
    } catch {
      /* ignore */
    }
    throw new Error(msg)
  }
  if (res.status === 204) return undefined as T
  return res.json() as Promise<T>
}

export const api = {
  me: () => request<User>('/api/auth/me'),
  login: (username: string, password: string) =>
    request<User>('/api/auth/login', {
      method: 'POST',
      body: JSON.stringify({ username, password }),
    }),
  logout: () => request<{ status: string }>('/api/auth/logout', { method: 'POST' }),
  listUsers: () => request<User[]>('/api/users'),
  createUser: (username: string, password: string, role: Role = 'user') =>
    request<User>('/api/users', {
      method: 'POST',
      body: JSON.stringify({ username, password, role }),
    }),
  renameUser: (id: number, username: string) =>
    request<User>(`/api/users/${id}`, {
      method: 'PATCH',
      body: JSON.stringify({ username }),
    }),
  updateDisplayName: (id: number, display_name: string) =>
    request<User>(`/api/users/${id}`, {
      method: 'PATCH',
      body: JSON.stringify({ display_name }),
    }),
  deleteUser: (id: number) =>
    request<{ status: string }>(`/api/users/${id}`, { method: 'DELETE' }),
  listTorrents: (ownerId?: number) => {
    const q = ownerId != null ? `?owner_id=${ownerId}` : ''
    return request<TorrentView[]>(`/api/torrents${q}`)
  },
  uploadTorrent: (file: File) => {
    const fd = new FormData()
    fd.append('torrent', file)
    return request<TorrentView>('/api/torrents', { method: 'POST', body: fd })
  },
  pause: (id: number) => request<TorrentView>(`/api/torrents/${id}/pause`, { method: 'POST' }),
  resume: (id: number) => request<TorrentView>(`/api/torrents/${id}/resume`, { method: 'POST' }),
  remove: (id: number) => request<{ status: string }>(`/api/torrents/${id}`, { method: 'DELETE' }),
  downloadUrl: (id: number) => `/api/torrents/${id}/download`,
  disk: () => request<DiskUsage>('/api/disk'),
}

export function formatBytes(n: number): string {
  if (!Number.isFinite(n) || n < 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let v = n
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(i === 0 ? 0 : 1)} ${units[i]}`
}

export function formatPct(p: number): string {
  return `${Math.min(100, Math.max(0, p * 100)).toFixed(1)}%`
}

export function formatRatio(uploaded: number, downloaded: number): string {
  if (downloaded <= 0) return uploaded > 0 ? '∞' : '—'
  return (uploaded / downloaded).toFixed(2)
}
