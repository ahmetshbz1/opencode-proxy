import { Activity, Clock, Gauge, RefreshCcw, Server } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import './App.css'

type UsageWindow = {
  used_percent: number
  limit_window_seconds: number
  reset_after_seconds: number
  reset_at: number
  reset_at_formatted?: string
}

type UsageSnapshot = {
  email?: string
  plan_type?: string
  allowed: boolean
  limit_reached: boolean
  rate_limit_reached_type?: string
  primary_window?: UsageWindow
  secondary_window?: UsageWindow
  fetched_at: string
}

type ProviderHealth = {
  name: string
  type: string
  priority: number
  models?: string[]
  exhausted: boolean
  exhausted_until?: string
  reset_in_seconds?: number
  usage?: UsageSnapshot
  usage_error?: string
}

type HealthResponse = {
  status: string
  port: number
  provider_count: number
  generated_at: string
  providers: ProviderHealth[]
}

type Tab = 'all' | 'codex' | 'limited' | 'ready'

function App() {
  const [health, setHealth] = useState<HealthResponse | null>(null)
  const [tab, setTab] = useState<Tab>('codex')
  const [error, setError] = useState<string | null>(null)

  const load = async (): Promise<void> => {
    try {
      const response = await fetch('/health.json', { headers: { Accept: 'application/json' } })
      if (!response.ok) throw new Error(`HTTP ${response.status}`)
      setHealth(await response.json())
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'health okunamadı')
    }
  }

  useEffect(() => {
    void load()
    const timer = window.setInterval(() => void load(), 30_000)
    return () => window.clearInterval(timer)
  }, [])

  const providers = useMemo(() => {
    const list = health?.providers ?? []
    return list.filter((provider) => {
      if (tab === 'all') return true
      if (tab === 'codex') return provider.type === 'codex'
      if (tab === 'limited') return provider.exhausted || provider.usage?.limit_reached
      return !provider.exhausted && !provider.usage?.limit_reached
    })
  }, [health, tab])

  return (
    <main className="min-h-screen bg-zinc-950 text-zinc-50">
      <section className="mx-auto max-w-7xl px-6 py-10">
        <header className="flex flex-col gap-6 border-b border-zinc-800 pb-8 lg:flex-row lg:items-end lg:justify-between">
          <div>
            <p className="text-xs uppercase tracking-[0.35em] text-zinc-500">opencode-proxy</p>
            <h1 className="mt-3 text-4xl font-black tracking-tight lg:text-6xl">Provider Health</h1>
          </div>
          <div className="text-sm text-zinc-400">{health?.generated_at ?? 'yükleniyor'}</div>
        </header>

        {error ? <div className="mt-6 border border-red-400 bg-red-950/40 p-4 text-red-100">{error}</div> : null}

        <section className="mt-8 grid gap-4 md:grid-cols-3">
          <Metric icon={<Activity />} label="Status" value={health?.status ?? '-'} />
          <Metric icon={<Server />} label="Port" value={health?.port?.toString() ?? '-'} />
          <Metric icon={<Gauge />} label="Providers" value={health?.provider_count?.toString() ?? '-'} />
        </section>

        <nav className="mt-8 flex flex-wrap gap-2 border-b border-zinc-800 pb-4">
          {(['all', 'codex', 'limited', 'ready'] as Tab[]).map((item) => (
            <button
              key={item}
              className={`border px-4 py-2 text-sm font-bold transition ${tab === item ? 'border-zinc-50 bg-zinc-50 text-zinc-950' : 'border-zinc-800 text-zinc-200 hover:border-zinc-400'}`}
              onClick={() => setTab(item)}
              type="button"
            >
              {tabTitle(item)}
            </button>
          ))}
          <button className="ml-auto flex items-center gap-2 border border-zinc-800 px-4 py-2 text-sm font-bold text-zinc-200 hover:border-zinc-400" onClick={() => void load()} type="button">
            <RefreshCcw className="h-4 w-4" /> Yenile
          </button>
        </nav>

        <section className="mt-6 grid gap-5">
          {providers.map((provider) => <ProviderCard key={provider.name} provider={provider} />)}
        </section>
      </section>
    </main>
  )
}

function Metric({ icon, label, value }: { icon: React.ReactNode; label: string; value: string }) {
  return (
    <article className="border border-zinc-800 bg-zinc-900/70 p-5">
      <div className="flex items-center gap-3 text-zinc-500">{icon}<span className="text-xs uppercase tracking-[0.2em]">{label}</span></div>
      <strong className="mt-4 block text-3xl">{value}</strong>
    </article>
  )
}

function ProviderCard({ provider }: { provider: ProviderHealth }) {
  const primary = provider.usage?.primary_window
  const secondary = provider.usage?.secondary_window
  const limited = provider.exhausted || provider.usage?.limit_reached

  return (
    <article className="border border-zinc-800 bg-zinc-900/70 p-5">
      <div className="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
        <div>
          <h2 className="break-all text-2xl font-black">{provider.name}</h2>
          <p className="mt-1 text-zinc-400">{provider.type} · priority {provider.priority}{provider.usage?.email ? ` · ${provider.usage.email}` : ''}</p>
        </div>
        <span className={`w-fit border px-4 py-2 text-sm font-black ${limited ? 'border-zinc-50 bg-zinc-950 text-zinc-50' : 'border-zinc-50 bg-zinc-50 text-zinc-950'}`}>{limited ? 'limited' : 'ready'}</span>
      </div>

      {provider.usage_error ? <div className="mt-5 border border-yellow-500/60 bg-yellow-950/30 p-3 text-sm text-yellow-100">usage okunamadı: {provider.usage_error}</div> : null}

      <div className="mt-5 grid gap-4 lg:grid-cols-2">
        <WindowCard title="5 saatlik limit" window={primary} fallbackSeconds={provider.reset_in_seconds} fallbackUntil={provider.exhausted_until} />
        <WindowCard title="Haftalık limit" window={secondary} />
      </div>

      <div className="mt-5 border-t border-zinc-800 pt-4">
        <p className="text-xs uppercase tracking-[0.2em] text-zinc-500">Models</p>
        <p className="mt-2 break-words text-zinc-100">{provider.models?.length ? provider.models.join(', ') : 'catch-all'}</p>
      </div>
    </article>
  )
}

function WindowCard({ title, window, fallbackSeconds, fallbackUntil }: { title: string; window?: UsageWindow; fallbackSeconds?: number; fallbackUntil?: string }) {
  const used = window?.used_percent
  const remaining = used === undefined ? undefined : Math.min(100, Math.max(0, 100 - used))
  const resetSeconds = window?.reset_after_seconds ?? fallbackSeconds
  const resetAt = window?.reset_at_formatted ?? fallbackUntil
  return (
    <div className="border border-zinc-800 bg-zinc-950 p-4">
      <div className="flex items-center justify-between gap-3">
        <p className="text-xs uppercase tracking-[0.2em] text-zinc-500">{title}</p>
        <Clock className="h-4 w-4 text-zinc-500" />
      </div>
      <div className="mt-4 flex items-end justify-between gap-4">
        <div>
          <strong className="text-3xl">{remaining === undefined ? '—' : `%${remaining}`}</strong>
          <span className="ml-2 text-sm text-zinc-500">kalan</span>
        </div>
        <span className="text-sm text-zinc-400">reset {formatDuration(resetSeconds)}</span>
      </div>
      <div className="mt-4 h-2 bg-zinc-800">
        <div className="h-full bg-zinc-50" style={{ width: `${remaining ?? 0}%` }} />
      </div>
      <p className="mt-3 text-xs text-zinc-500">{resetAt ?? 'reset bilgisi yok'}</p>
    </div>
  )
}

function tabTitle(tab: Tab): string {
  if (tab === 'all') return 'Tümü'
  if (tab === 'codex') return 'Codex'
  if (tab === 'limited') return 'Limitli'
  return 'Hazır'
}

function formatDuration(seconds?: number): string {
  if (!seconds || seconds <= 0) return 'bilinmiyor'
  const hours = Math.floor(seconds / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)
  if (hours > 0) return `${hours}s ${minutes}dk`
  return `${minutes}dk`
}

export default App
