import { useEffect, useMemo, useState, type FormEvent } from 'react'
import { api, formatBytes, formatPct, type DiskUsage, type TorrentStatus, type TorrentView, type User } from './api'
import './App.css'

type Filter = 'all' | TorrentStatus
type Panel = 'torrents' | 'users'

export default function App() {
  const [user, setUser] = useState<User | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  useEffect(() => {
    api
      .me()
      .then(setUser)
      .catch(() => setUser(null))
      .finally(() => setLoading(false))
  }, [])

  if (loading) {
    return (
      <div className="boot">
        <p>Loading…</p>
      </div>
    )
  }

  if (!user) {
    return (
      <Login
        onLogin={(u) => {
          setError('')
          setUser(u)
        }}
        error={error}
        setError={setError}
      />
    )
  }

  return (
    <Dashboard
      user={user}
      onLogout={async () => {
        await api.logout()
        setUser(null)
      }}
    />
  )
}

const REMEMBER_KEY = 'seedly.remember'

function loadRemembered(): { username: string; password: string } | null {
  try {
    const raw = localStorage.getItem(REMEMBER_KEY)
    if (!raw) return null
    const parsed = JSON.parse(raw) as { username?: unknown; password?: unknown }
    if (typeof parsed.username !== 'string' || typeof parsed.password !== 'string') {
      return null
    }
    return { username: parsed.username, password: parsed.password }
  } catch {
    return null
  }
}

function Login({
  onLogin,
  error,
  setError,
}: {
  onLogin: (u: User) => void
  error: string
  setError: (s: string) => void
}) {
  const saved = loadRemembered()
  const [username, setUsername] = useState(saved?.username ?? '')
  const [password, setPassword] = useState(saved?.password ?? '')
  const [remember, setRemember] = useState(Boolean(saved))
  const [busy, setBusy] = useState(false)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      const u = await api.login(username, password)
      if (remember) {
        localStorage.setItem(REMEMBER_KEY, JSON.stringify({ username, password }))
      } else {
        localStorage.removeItem(REMEMBER_KEY)
      }
      onLogin(u)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Login failed')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="boot">
      <form className="login-card" onSubmit={submit}>
        <div className="brand-row">
          <img className="brand-mark" src="/logo.png" alt="" width={36} height={36} />
          <div className="brand">Seedly</div>
        </div>
        <label>
          Username
          <input
            autoComplete="username"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            required
          />
        </label>
        <label>
          Password
          <input
            type="password"
            autoComplete="current-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required
          />
        </label>
        <label className="remember-row">
          <input
            type="checkbox"
            checked={remember}
            onChange={(e) => setRemember(e.target.checked)}
          />
          Remember me
        </label>
        {error ? <p className="error">{error}</p> : null}
        <button type="submit" disabled={busy}>
          {busy ? 'Signing in…' : 'Sign in'}
        </button>
      </form>
    </div>
  )
}

function Dashboard({ user, onLogout }: { user: User; onLogout: () => void }) {
  const [panel, setPanel] = useState<Panel>('torrents')
  const [torrents, setTorrents] = useState<TorrentView[]>([])
  const [users, setUsers] = useState<User[]>([])
  const [viewOwnerId, setViewOwnerId] = useState<number>(user.id)
  const [filter, setFilter] = useState<Filter>('all')
  const [selectedId, setSelectedId] = useState<number | null>(null)
  const [error, setError] = useState('')
  const [disk, setDisk] = useState<DiskUsage | null>(null)

  const isAdmin = user.role === 'admin'

  async function refreshTorrents() {
    try {
      const list = await api.listTorrents(isAdmin && viewOwnerId !== user.id ? viewOwnerId : undefined)
      setTorrents(list)
      setError('')
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load torrents')
    }
  }

  async function refreshUsers() {
    if (!isAdmin) return
    try {
      setUsers(await api.listUsers())
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load users')
    }
  }

  async function refreshDisk() {
    try {
      setDisk(await api.disk())
    } catch {
      /* keep last known */
    }
  }

  useEffect(() => {
    if (panel !== 'torrents') return
    void refreshTorrents()
    const t = setInterval(() => {
      void refreshTorrents()
    }, 1500)
    return () => clearInterval(t)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [viewOwnerId, user.id, isAdmin, panel])

  useEffect(() => {
    void refreshDisk()
    const t = setInterval(() => {
      void refreshDisk()
    }, 10000)
    return () => clearInterval(t)
  }, [])

  useEffect(() => {
    if (!isAdmin) return
    void refreshUsers()
  }, [isAdmin, panel])

  const counts = useMemo(() => {
    const c = { all: torrents.length, downloading: 0, seeding: 0, paused: 0, error: 0 }
    for (const t of torrents) {
      c[t.status] += 1
    }
    return c
  }, [torrents])

  const visible = useMemo(
    () => (filter === 'all' ? torrents : torrents.filter((t) => t.status === filter)),
    [torrents, filter],
  )

  const selected = torrents.find((t) => t.id === selectedId) ?? null

  async function onUpload(file: File | null) {
    if (!file) return
    try {
      await api.uploadTorrent(file)
      await refreshTorrents()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Upload failed')
    }
  }

  const filters: { id: Filter; label: string }[] = [
    { id: 'all', label: 'All' },
    { id: 'downloading', label: 'Downloading' },
    { id: 'seeding', label: 'Seeding' },
    { id: 'paused', label: 'Paused' },
    { id: 'error', label: 'Error' },
  ]

  return (
    <div className="app">
      <aside className="sidebar">
        <div className="sidebar-brand">
          <img className="brand-mark" src="/logo.png" alt="" width={28} height={28} />
          <span>Seedly</span>
        </div>

        <nav className="sidebar-nav" aria-label="Main">
          <button
            type="button"
            className={panel === 'torrents' ? 'nav-item active' : 'nav-item'}
            onClick={() => setPanel('torrents')}
          >
            <span>Torrents</span>
            <span className="count">{counts.all}</span>
          </button>
          {isAdmin ? (
            <button
              type="button"
              className={panel === 'users' ? 'nav-item active' : 'nav-item'}
              onClick={() => setPanel('users')}
            >
              <span>Users</span>
              <span className="count">{users.length}</span>
            </button>
          ) : null}
        </nav>

        {panel === 'torrents' ? (
          <nav className="sidebar-nav sidebar-subnav" aria-label="Torrent filters">
            <div className="sidebar-label">Status</div>
            {filters.map((f) => (
              <button
                key={f.id}
                type="button"
                className={filter === f.id ? 'nav-item active' : 'nav-item'}
                onClick={() => setFilter(f.id)}
              >
                <span>{f.label}</span>
                <span className="count">{counts[f.id]}</span>
              </button>
            ))}
          </nav>
        ) : null}

        {isAdmin && panel === 'torrents' ? (
          <div className="sidebar-block">
            <div className="sidebar-label">View as</div>
            <select
              value={viewOwnerId}
              onChange={(e) => setViewOwnerId(Number(e.target.value))}
            >
              <option value={user.id}>Me ({user.username})</option>
              {users
                .filter((u) => u.id !== user.id)
                .map((u) => (
                  <option key={u.id} value={u.id}>
                    {u.username}
                  </option>
                ))}
            </select>
          </div>
        ) : null}

        <div className="sidebar-bottom">
          {disk && disk.total_bytes > 0 ? (
            <div className="disk-usage" title={disk.path}>
              <div className="sidebar-label">Disk</div>
              <div className="disk-bar" role="img" aria-label="Disk usage">
                <span
                  className="seg seedly"
                  style={{ width: `${(disk.seedly_bytes / disk.total_bytes) * 100}%` }}
                />
                <span
                  className="seg other"
                  style={{ width: `${(disk.other_bytes / disk.total_bytes) * 100}%` }}
                />
                <span
                  className="seg free"
                  style={{ width: `${(disk.free_bytes / disk.total_bytes) * 100}%` }}
                />
              </div>
              <div className="disk-legend">
                <span>
                  <i className="dot seedly" /> Seedly {formatBytes(disk.seedly_bytes)}
                </span>
                <span>
                  <i className="dot other" /> Other {formatBytes(disk.other_bytes)}
                </span>
                <span>
                  <i className="dot free" /> Free {formatBytes(disk.free_bytes)}
                </span>
              </div>
            </div>
          ) : null}

          <div className="sidebar-footer">
            <div className="user-meta">
              <strong>{user.username}</strong>
              <span className="muted">{user.role}</span>
            </div>
            <button type="button" className="ghost" onClick={onLogout}>
              Sign out
            </button>
          </div>
        </div>
      </aside>

      <main className="main">
        {panel === 'torrents' ? (
          <>
            <header className="topbar">
              <div className="topbar-title">
                <h1>Torrents</h1>
                <span className="muted">
                  {visible.length} shown
                  {filter !== 'all' ? ` · ${filter}` : ''}
                </span>
              </div>
              <label className="add-torrent">
                <span>+ Add torrent</span>
                <input
                  type="file"
                  accept=".torrent,application/x-bittorrent"
                  onChange={(e) => {
                    const f = e.target.files?.[0] ?? null
                    void onUpload(f)
                    e.target.value = ''
                  }}
                />
              </label>
            </header>

            {error ? <p className="error banner">{error}</p> : null}

            <div className="content">
              <div className="torrent-list">
                {visible.length === 0 ? (
                  <div className="empty">No torrents in this view. Add a .torrent to get started.</div>
                ) : (
                  <table>
                    <thead>
                      <tr>
                        <th className="col-name">Name</th>
                        <th className="col-status">Status</th>
                        <th className="col-progress">Progress</th>
                        <th className="col-num">↓</th>
                        <th className="col-num">↑</th>
                        <th className="col-peers">Peers</th>
                        <th className="col-size">Size</th>
                      </tr>
                    </thead>
                    <tbody>
                      {visible.map((t) => (
                        <tr
                          key={t.id}
                          className={selectedId === t.id ? 'selected' : undefined}
                          onClick={() => setSelectedId(t.id)}
                        >
                          <td className="col-name">
                            <div className="name">{t.name}</div>
                            <div className="hash">{t.info_hash.slice(0, 16)}</div>
                          </td>
                          <td className="col-status">
                            <span className={`pill ${t.status}`}>{t.status}</span>
                          </td>
                          <td className="col-progress">
                            <div className="progress">
                              <div className="bar" style={{ width: formatPct(t.stats.progress) }} />
                            </div>
                            <div className="pct">{formatPct(t.stats.progress)}</div>
                          </td>
                          <td className="col-num">{formatBytes(t.stats.downloaded)}</td>
                          <td className="col-num">{formatBytes(t.stats.uploaded)}</td>
                          <td className="col-peers">{t.stats.peers}</td>
                          <td className="col-size">{formatBytes(t.stats.total_length)}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                )}
              </div>

              <aside className="detail">
                {selected ? (
                  <>
                    <h2>{selected.name}</h2>
                    <dl>
                      <div>
                        <dt>Status</dt>
                        <dd>{selected.status}</dd>
                      </div>
                      <div>
                        <dt>Progress</dt>
                        <dd>
                          {formatPct(selected.stats.progress)} ·{' '}
                          {formatBytes(selected.stats.bytes_completed)} /{' '}
                          {formatBytes(selected.stats.total_length)}
                        </dd>
                      </div>
                      <div>
                        <dt>Downloaded</dt>
                        <dd>{formatBytes(selected.stats.downloaded)}</dd>
                      </div>
                      <div>
                        <dt>Uploaded</dt>
                        <dd>{formatBytes(selected.stats.uploaded)}</dd>
                      </div>
                      <div>
                        <dt>Peers</dt>
                        <dd>{selected.stats.peers}</dd>
                      </div>
                      <div>
                        <dt>Info hash</dt>
                        <dd className="mono">{selected.info_hash}</dd>
                      </div>
                      {selected.error_message ? (
                        <div>
                          <dt>Error</dt>
                          <dd className="error">{selected.error_message}</dd>
                        </div>
                      ) : null}
                    </dl>
                    <div className="detail-actions">
                      {selected.status === 'paused' ? (
                        <button
                          type="button"
                          onClick={() => api.resume(selected.id).then(refreshTorrents)}
                        >
                          Resume
                        </button>
                      ) : (
                        <button
                          type="button"
                          onClick={() => api.pause(selected.id).then(refreshTorrents)}
                        >
                          Pause
                        </button>
                      )}
                      {(selected.stats.complete ||
                        selected.status === 'seeding' ||
                        selected.completed_at) && (
                        <a className="button" href={api.downloadUrl(selected.id)}>
                          Download
                        </a>
                      )}
                      <button
                        type="button"
                        className="danger"
                        onClick={() => {
                          if (confirm(`Delete “${selected.name}”?`)) {
                            void api.remove(selected.id).then(() => {
                              setSelectedId(null)
                              return refreshTorrents()
                            })
                          }
                        }}
                      >
                        Delete
                      </button>
                    </div>
                  </>
                ) : (
                  <p className="muted">Select a torrent to manage it.</p>
                )}
              </aside>
            </div>
          </>
        ) : (
          <UsersPanel
            currentUser={user}
            users={users}
            onChange={refreshUsers}
            onError={setError}
            error={error}
          />
        )}
      </main>
    </div>
  )
}

function UsersPanel({
  currentUser,
  users,
  onChange,
  onError,
  error,
}: {
  currentUser: User
  users: User[]
  onChange: () => Promise<void>
  onError: (msg: string) => void
  error: string
}) {
  const [newUser, setNewUser] = useState({ username: '', password: '', role: 'user' as 'user' | 'admin' })
  const [editingId, setEditingId] = useState<number | null>(null)
  const [editName, setEditName] = useState('')
  const [busy, setBusy] = useState(false)

  async function createUser(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    try {
      await api.createUser(newUser.username, newUser.password, newUser.role)
      setNewUser({ username: '', password: '', role: 'user' })
      onError('')
      await onChange()
    } catch (err) {
      onError(err instanceof Error ? err.message : 'Create user failed')
    } finally {
      setBusy(false)
    }
  }

  async function saveRename(id: number) {
    setBusy(true)
    try {
      await api.renameUser(id, editName.trim())
      setEditingId(null)
      onError('')
      await onChange()
    } catch (err) {
      onError(err instanceof Error ? err.message : 'Rename failed')
    } finally {
      setBusy(false)
    }
  }

  async function removeUser(u: User) {
    if (u.id === currentUser.id) {
      onError('Cannot delete your own account')
      return
    }
    if (!confirm(`Delete user “${u.username}”? Their torrents will be removed.`)) {
      return
    }
    setBusy(true)
    try {
      await api.deleteUser(u.id)
      onError('')
      await onChange()
    } catch (err) {
      onError(err instanceof Error ? err.message : 'Delete failed')
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
      <header className="topbar">
        <div className="topbar-title">
          <h1>Users</h1>
          <span className="muted">{users.length} accounts · quotas coming later</span>
        </div>
      </header>

      {error ? <p className="error banner">{error}</p> : null}

      <div className="users-panel">
        <section className="users-card">
          <h2>Add user</h2>
          <form className="users-form" onSubmit={createUser}>
            <input
              placeholder="username"
              value={newUser.username}
              onChange={(e) => setNewUser((s) => ({ ...s, username: e.target.value }))}
              required
            />
            <input
              type="password"
              placeholder="password"
              value={newUser.password}
              onChange={(e) => setNewUser((s) => ({ ...s, password: e.target.value }))}
              required
            />
            <select
              value={newUser.role}
              onChange={(e) =>
                setNewUser((s) => ({ ...s, role: e.target.value as 'user' | 'admin' }))
              }
            >
              <option value="user">user</option>
              <option value="admin">admin</option>
            </select>
            <button type="submit" disabled={busy}>
              Add
            </button>
          </form>
        </section>

        <section className="users-card users-table-card">
          <h2>Manage users</h2>
          <div className="torrent-list">
            <table>
              <thead>
                <tr>
                  <th>Username</th>
                  <th>Role</th>
                  <th>Created</th>
                  <th>Actions</th>
                </tr>
              </thead>
              <tbody>
                {users.map((u) => (
                  <tr key={u.id}>
                    <td>
                      {editingId === u.id ? (
                        <input
                          value={editName}
                          onChange={(e) => setEditName(e.target.value)}
                          autoFocus
                        />
                      ) : (
                        <div className="name">
                          {u.username}
                          {u.id === currentUser.id ? (
                            <span className="muted"> · you</span>
                          ) : null}
                        </div>
                      )}
                    </td>
                    <td>
                      <span className={`pill ${u.role}`}>{u.role}</span>
                    </td>
                    <td className="col-num">{new Date(u.created_at).toLocaleString()}</td>
                    <td>
                      <div className="detail-actions">
                        {editingId === u.id ? (
                          <>
                            <button
                              type="button"
                              disabled={busy || !editName.trim()}
                              onClick={() => void saveRename(u.id)}
                            >
                              Save
                            </button>
                            <button
                              type="button"
                              className="ghost"
                              onClick={() => setEditingId(null)}
                            >
                              Cancel
                            </button>
                          </>
                        ) : (
                          <>
                            <button
                              type="button"
                              className="ghost"
                              onClick={() => {
                                setEditingId(u.id)
                                setEditName(u.username)
                              }}
                            >
                              Rename
                            </button>
                            <button
                              type="button"
                              className="danger"
                              disabled={busy || u.id === currentUser.id}
                              onClick={() => void removeUser(u)}
                            >
                              Delete
                            </button>
                          </>
                        )}
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <p className="muted users-hint">
            Future settings (quotas, bandwidth limits, …) will appear here per user.
          </p>
        </section>
      </div>
    </>
  )
}
