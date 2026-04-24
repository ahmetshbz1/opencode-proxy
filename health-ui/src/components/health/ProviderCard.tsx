import type { ProviderHealth } from '../../domain/health/types'
import { formatCacheAge } from '../../domain/health/format'
import { Badge } from '../ui/badge'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '../ui/card'
import { Alert } from './Alert'
import { Skeleton } from './Skeleton'
import { WindowCard, WindowCardSkeleton } from './WindowCard'

export function ProviderCard({ provider }: { provider: ProviderHealth }) {
  const primary = provider.usage?.primary_window
  const secondary = provider.usage?.secondary_window
  const limited = provider.exhausted || provider.usage?.limit_reached

  return (
    <Card>
      <CardHeader className="grid-cols-[1fr_auto]">
        <div>
          <CardTitle className="break-all text-2xl font-black tracking-tight">{provider.name}</CardTitle>
          <CardDescription className="mt-1">{provider.type} · priority {provider.priority}{provider.usage?.email ? ` · ${provider.usage.email}` : ''}{provider.usage ? ` · usage ${formatCacheAge(provider.usage.cache_age_seconds)}` : ''}</CardDescription>
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

export function ProviderCardSkeleton() {
  return (
    <Card>
      <CardHeader className="grid-cols-[1fr_auto]">
        <div className="space-y-3">
          <Skeleton className="h-7 w-72 max-w-full" />
          <Skeleton className="h-4 w-52 max-w-full" />
        </div>
        <Skeleton className="h-7 w-20 rounded-md" />
      </CardHeader>
      <CardContent>
        <div className="mt-5 grid gap-4 lg:grid-cols-2">
          <WindowCardSkeleton />
          <WindowCardSkeleton />
        </div>
        <div className="mt-5 border-t border-border pt-4">
          <Skeleton className="h-3 w-20" />
          <Skeleton className="mt-3 h-5 w-96 max-w-full" />
        </div>
      </CardContent>
    </Card>
  )
}
