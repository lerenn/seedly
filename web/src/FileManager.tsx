import { useCallback, useEffect, useState } from 'react'
import { api, formatBytes, type FileEntry } from './api'

function join(dir: string, name: string): string {
  return dir === '/' ? `/${name}` : `${dir}/${name}`
}

function parent(dir: string): string {
  const i = dir.lastIndexOf('/')
  return i <= 0 ? '/' : dir.slice(0, i)
}

export function FileManager({ torrentId }: { torrentId: number }) {
  const [dir, setDir] = useState('/')
  const [entries, setEntries] = useState<FileEntry[]>([])
  const [writable, setWritable] = useState(false)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const load = useCallback(
    async (path: string) => {
      try {
        const res = await api.listFiles(torrentId, path)
        setDir(res.path)
        setEntries(res.entries)
        setWritable(res.writable)
        setError('')
      } catch (err) {
        setError(err instanceof Error ? err.message : 'Could not list files')
      }
    },
    [torrentId],
  )

  useEffect(() => {
    void load('/')
  }, [load])

  async function run(op: () => Promise<unknown>) {
    setBusy(true)
    try {
      await op()
      await load(dir)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Operation failed')
    } finally {
      setBusy(false)
    }
  }

  const crumbs = dir === '/' ? [] : dir.slice(1).split('/')

  return (
    <section className="file-manager">
      <h3>Files</h3>

      <nav className="fm-crumbs" aria-label="Path">
        <button type="button" onClick={() => void load('/')}>
          /
        </button>
        {crumbs.map((c, i) => (
          <span key={i}>
            <button type="button" onClick={() => void load('/' + crumbs.slice(0, i + 1).join('/'))}>
              {c}
            </button>
            {i < crumbs.length - 1 ? ' / ' : ''}
          </span>
        ))}
      </nav>

      {writable ? (
        <div className="detail-actions fm-actions">
          <label className="button fm-upload">
            Upload
            <input
              type="file"
              multiple
              disabled={busy}
              onChange={(e) => {
                const picked = Array.from(e.target.files ?? [])
                e.target.value = ''
                if (picked.length) void run(() => api.uploadFiles(torrentId, dir, picked))
              }}
            />
          </label>
          <button
            type="button"
            disabled={busy}
            onClick={() => {
              const name = prompt('Folder name')?.trim()
              if (name) void run(() => api.mkdir(torrentId, join(dir, name)))
            }}
          >
            New folder
          </button>
        </div>
      ) : null}

      {error ? <p className="error">{error}</p> : null}

      <ul className="fm-list">
        {dir !== '/' ? (
          <li>
            <button type="button" className="fm-name" onClick={() => void load(parent(dir))}>
              📁 ..
            </button>
          </li>
        ) : null}
        {entries.map((e) => {
          const full = join(dir, e.name)
          return (
            <li key={e.name}>
              {e.is_dir ? (
                <button type="button" className="fm-name" onClick={() => void load(full)}>
                  📁 {e.name}
                </button>
              ) : (
                <a className="fm-name" href={api.fileDownloadUrl(torrentId, full)} download>
                  📄 {e.name}
                </a>
              )}
              {e.is_dir ? null : <span className="muted fm-size">{formatBytes(e.size)}</span>}
              {writable ? (
                <span className="fm-row-actions">
                  <button
                    type="button"
                    disabled={busy}
                    onClick={() => {
                      const to = prompt('New name', e.name)?.trim()
                      if (to && to !== e.name) {
                        void run(() => api.renameFile(torrentId, full, join(dir, to)))
                      }
                    }}
                  >
                    Rename
                  </button>
                  <button
                    type="button"
                    className="danger"
                    disabled={busy}
                    onClick={() => {
                      if (confirm(`Delete “${e.name}”?`)) void run(() => api.deleteFile(torrentId, full))
                    }}
                  >
                    Delete
                  </button>
                </span>
              ) : null}
            </li>
          )
        })}
        {entries.length === 0 ? <li className="muted">Empty</li> : null}
      </ul>
    </section>
  )
}
