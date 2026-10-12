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
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [progress, setProgress] = useState('')
  const [selected, setSelected] = useState<Map<string, boolean>>(new Map()) // path -> is_dir

  const load = useCallback(
    async (path: string) => {
      try {
        const res = await api.listFiles(torrentId, path)
        setDir(res.path)
        setEntries(res.entries)
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

  function toggle(path: string, isDir: boolean) {
    setSelected((cur) => {
      const next = new Map(cur)
      if (next.has(path)) next.delete(path)
      else next.set(path, isDir)
      return next
    })
  }

  // Starts one browser download per selected file (folders are expanded);
  // browsers may ask to allow multiple downloads.
  async function downloadSelected() {
    setBusy(true)
    try {
      const paths: string[] = []
      for (const [sel, isDir] of selected) {
        if (isDir) paths.push(...(await collectFiles(torrentId, sel)))
        else paths.push(sel)
      }
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
          <button
            type="button"
            disabled={busy || selected.size === 0}
            onClick={() => void downloadSelected()}
          >
            {progress || `Download Selected${selected.size ? ` (${selected.size})` : ''}`}
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
              <input
                type="checkbox"
                className="fm-check"
                aria-label={`Select ${e.name}`}
                checked={selected.has(full)}
                onChange={() => toggle(full, e.is_dir)}
              />
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
            </li>
          )
        })}
        {entries.length === 0 ? <li className="muted">Empty</li> : null}
      </ul>
    </section>
  )
}
