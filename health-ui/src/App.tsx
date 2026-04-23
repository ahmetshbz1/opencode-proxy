import { Activity, Clock, Gauge, RefreshCcw, Server } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { Badge } from './components/ui/badge'
import { Button } from './components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from './components/ui/card'
import { cn } from './lib/utils'
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
type Tone = 'success' | 'warning' | 'danger' | 'muted'

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
          <Metric icon={<Activity className="h-6 w-6" />} label="Status" value={health?.status ?? '-'} />
          <Metric icon={<Server className="h-6 w-6" />} label="Port" value={health?.port?.toString() ?? '-'} />
          <Metric icon={<Gauge className="h-6 w-6" />} label="Providers" value={health?.provider_count?.toString() ?? '-'} />
        </section>

        <Tabs value={tab} onValueChange={setTab} onRefresh={() => void load()} />

        <section className="mt-6 grid gap-5">
          {providers.map((provider) => <ProviderCard key={provider.name} provider={provider} />)}
        </section>
      </section>
    </main>
  )
}

function Metric({ icon, label, value }: { icon: React.ReactNode; label: string; value: string }) {
  return (
    <Card>
      <CardHeader>
        <div className="flex items-center gap-3 text-muted-foreground">{icon}<span className="text-xs uppercase tracking-[0.2em]">{label}</span></div>
      </CardHeader>
      <CardContent>
        <strong className="block text-3xl font-black tracking-tight">{value}</strong>
      </CardContent>
    </Card>
  )
}

function ProviderCard({ provider }: { provider: ProviderHealth }) {
  const primary = provider.usage?.primary_window
  const secondary = provider.usage?.secondary_window
  const limited = provider.exhausted || provider.usage?.limit_reached

  return (
    <Card>
      <CardHeader className="grid-cols-[1fr_auto]">
        <div>
          <CardTitle className="break-all text-2xl font-black tracking-tight">{provider.name}</CardTitle>
          <CardDescription className="mt-1">{provider.type} · priority {provider.priority}{provider.usage?.email ? ` · ${provider.usage.email}` : ''}</CardDescription>
        </div>
        <Badge variant={limited ? 'destructive' : 'success'} size="lg">{limited ? 'limited' : 'ready'}</Badge>
      </CardHeader>
      <CardContent>
        {provider.usage_error ? <Alert>usage okunamadı: {provider.usage_error}</Alert> : null}

        <div className="mt-5 grid gap-4 lg:grid-cols-2">
          <WindowCard title="5 saatlik limit" window={primary} fallbackSeconds={provider.reset_in_seconds} fallbackUntil={provider.exhausted_until} />
          <WindowCard title="Haftalık limit" window={secondary} />
        </div>

        <div className="mt-5 border-t border-border pt-4">
          <p className="text-xs uppercase tracking-[0.2em] text-muted-foreground">Models</p>
          <p className="mt-2 break-words text-foreground">{provider.models?.length ? provider.models.join(', ') : 'catch-all'}</p>
        </div>
      </CardContent>
    </Card>
  )
}

function WindowCard({ title, window, fallbackSeconds, fallbackUntil }: { title: string; window?: UsageWindow; fallbackSeconds?: number; fallbackUntil?: string }) {
  const used = window?.used_percent
  const remaining = used === undefined ? undefined : Math.min(100, Math.max(0, 100 - used))
  const resetSeconds = window?.reset_after_seconds ?? fallbackSeconds
  const resetAt = window?.reset_at_formatted ?? fallbackUntil
  const tone = quotaTone(remaining)

  return (
    <Card className="bg-popover/70">
      <CardHeader className="pb-3">
        <div className="flex items-center justify-between gap-3">
          <p className="text-xs uppercase tracking-[0.2em] text-muted-foreground">{title}</p>
          <Clock className="h-4 w-4 text-muted-foreground" />
        </div>
      </CardHeader>
      <CardContent>
        <div className="flex items-end justify-between gap-4">
          <div>
            <strong className={cn('text-3xl font-black tracking-tight', quotaTextClass(tone))}>{remaining === undefined ? '—' : `%${remaining}`}</strong>
            <span className="ml-2 text-sm text-muted-foreground">kalan</span>
          </div>
          <span className="text-sm text-muted-foreground">reset {formatDuration(resetSeconds)}</span>
        </div>
        <Progress value={remaining ?? 0} tone={tone} className="mt-4" />
        <p className="mt-3 text-xs text-muted-foreground">{resetAt ?? 'reset bilgisi yok'}</p>
      </CardContent>
    </Card>
  )
}

function Tabs({ value, onValueChange, onRefresh }: { value: Tab; onValueChange: (tab: Tab) => void; onRefresh: () => void }) {
  return (
    <nav className="mt-8 flex flex-wrap gap-2 border-b border-zinc-800 pb-4">
      {(['all', 'codex', 'limited', 'ready'] as Tab[]).map((item) => (
        <Button key={item} variant={value === item ? 'default' : 'outline'} onClick={() => onValueChange(item)}>
          {tabTitle(item)}
        </Button>
      ))}
      <Button className="ml-auto" variant="outline" onClick={onRefresh}>
        <RefreshCcw className="h-4 w-4" /> Yenile
      </Button>
    </nav>
  )
}

function Progress({ value, tone, className }: { value: number; tone: Tone; className?: string }) {
  return (
    <div className={cn('h-2 overflow-hidden rounded-full bg-zinc-800', className)}>
      <div className={cn('h-full rounded-full transition-all', quotaBarClass(tone))} style={{ width: `${value}%` }} />
    </div>
  )
}

function Alert({ children }: { children: React.ReactNode }) {
  return <div className="mt-5 rounded-lg border border-yellow-500/60 bg-yellow-950/30 p-3 text-sm text-yellow-100">{children}</div>
}

function tabTitle(tab: Tab): string {
  if (tab === 'all') return 'Tümü'
  if (tab === 'codex') return 'Codex'
  if (tab === 'limited') return 'Limitli'
  return 'Hazır'
}

function quotaTone(remaining?: number): Tone {
  if (remaining === undefined) return 'muted'
  if (remaining <= 20) return 'danger'
  if (remaining <= 50) return 'warning'
  return 'success'
}

function quotaTextClass(tone: Tone): string {
  if (tone === 'success') return 'text-emerald-300'
  if (tone === 'warning') return 'text-yellow-300'
  if (tone === 'danger') return 'text-red-300'
  return 'text-zinc-100'
}

function quotaBarClass(tone: Tone): string {
  if (tone === 'success') return 'bg-emerald-400'
  if (tone === 'warning') return 'bg-yellow-400'
  if (tone === 'danger') return 'bg-red-500'
  return 'bg-zinc-500'
}

function formatDuration(seconds?: number): string {
  if (!seconds || seconds <= 0) return 'bilinmiyor'
  const hours = Math.floor(seconds / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)
  if (hours > 0) return `${hours}s ${minutes}dk`
  return `${minutes}dk`
}

export default App
