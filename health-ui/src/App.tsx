import { Activity, Gauge, Server } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { Alert } from './components/health/Alert'
import { HealthTabs } from './components/health/HealthTabs'
import { Metric, MetricSkeleton } from './components/health/Metric'
import { ProviderCard, ProviderCardSkeleton } from './components/health/ProviderCard'
import type { HealthResponse, HealthTab } from './domain/health/types'
import './App.css'

function App() {
  const [health, setHealth] = useState<HealthResponse | null>(null)
  const [tab, setTab] = useState<HealthTab>('codex')
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

  const loading = health === null && error === null

  return (
    <main className="dark min-h-screen bg-background text-foreground">
      <section className="mx-auto max-w-7xl px-5 py-8 sm:px-6 lg:px-8 lg:py-10">
        <header className="flex flex-col gap-6 border-b border-zinc-800 pb-8 lg:flex-row lg:items-end lg:justify-between">
          <div>
            <p className="text-xs uppercase tracking-[0.35em] text-zinc-500">opencode-proxy</p>
            <h1 className="mt-3 text-4xl font-black tracking-tight lg:text-6xl">Provider Health</h1>
          </div>
          <div className="text-sm text-zinc-400">{health?.generated_at ?? 'yükleniyor'}</div>
        </header>

        {error ? <Alert>{error}</Alert> : null}

        <section className="mt-8 grid gap-4 md:grid-cols-3">
          {loading ? (
            <>
              <MetricSkeleton />
              <MetricSkeleton />
              <MetricSkeleton />
            </>
          ) : (
            <>
              <Metric icon={<Activity className="h-6 w-6" />} label="Status" value={health?.status ?? '-'} />
              <Metric icon={<Server className="h-6 w-6" />} label="Port" value={health?.port?.toString() ?? '-'} />
              <Metric icon={<Gauge className="h-6 w-6" />} label="Providers" value={health?.provider_count?.toString() ?? '-'} />
            </>
          )}
        </section>

        <HealthTabs value={tab} onValueChange={setTab} onRefresh={() => void load()} />

        <section className="mt-6 grid gap-5">
          {loading ? (
            <>
              <ProviderCardSkeleton />
              <ProviderCardSkeleton />
              <ProviderCardSkeleton />
            </>
          ) : providers.map((provider) => <ProviderCard key={provider.name} provider={provider} />)}
        </section>
      </section>
    </main>
  )
}

export default App
