import { useCallback, useEffect, useState } from 'react'
import { api, formatBytes, type FileEntry } from './api'

function join(dir: string, name: string): string {
  return dir === '/' ? `/${name}` : `${dir}/${name}`
}

function parent(dir: string): string {
  const i = dir.lastIndexOf('/')
  return i <= 0 ? '/' : dir.slice(0, i)
}

async function collectFiles(torrentId: number, dir: string): Promise<string[]> {
  const res = await api.listFiles(torrentId, dir)
  const out: string[] = []
  for (const e of res.entries) {
    const full = join(dir, e.name)
    if (e.is_dir) out.push(...(await collectFiles(torrentId, full)))
    else out.push(full)
  }
  return out
}

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms))

export function FileManager({ torrentId, zipUrl }: { torrentId: number; zipUrl?: string }) {
  const [dir, setDir] = useState('/')
  const [entries, setEntries] = useState<FileEntry[]>([])
  const [writable, setWritable] = useState(false)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [progress, setProgress] = useState('')

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

  // Starts one browser download per file; browsers may ask to allow multiple downloads.
  async function downloadSeparately() {
    setBusy(true)
    try {
      const paths = await collectFiles(torrentId, '/')
      for (let i = 0; i < paths.length; i++) {
        setProgress(`Downloading ${i + 1}/${paths.length}…`)
        const a = document.createElement('a')
        a.href = api.fileDownloadUrl(torrentId, paths[i])
        a.download = ''
        document.body.appendChild(a)
        a.click()
        a.remove()
        await sleep(400)
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Download failed')
    } finally {
      setProgress('')
      setBusy(false)
    }
  }

  const crumbs = dir === '/' ? [] : dir.slice(1).split('/')

  return (
    <section className="file-manager">
      <h3>Files</h3>

      {zipUrl ? (
        <div className="fm-downloads">
          <a className="button" href={zipUrl}>
            Download All (ZIP)
          </a>
          <button type="button" disabled={busy} onClick={() => void downloadSeparately()}>
            {progress || 'Download All (Separately)'}
          </button>
        </div>
      ) : null}

      {dir !== '/' ? (
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
