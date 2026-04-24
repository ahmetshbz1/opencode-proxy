import { Clock } from 'lucide-react'
import type { UsageWindow } from '../../domain/health/types'
import { formatDuration } from '../../domain/health/format'
import { quotaTextClass, quotaTone } from '../../domain/health/quota'
import { cn } from '../../lib/utils'
import { Card, CardContent, CardHeader } from '../ui/card'
import { Progress } from './Progress'
import { Skeleton } from './Skeleton'

export function WindowCard({ title, window, fallbackSeconds, fallbackUntil }: { title: string; window?: UsageWindow; fallbackSeconds?: number; fallbackUntil?: string }) {
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

export function WindowCardSkeleton() {
  return (
    <Card className="bg-popover/70">
      <CardHeader className="pb-3">
        <div className="flex items-center justify-between gap-3">
          <Skeleton className="h-3 w-32" />
          <Skeleton className="size-4 rounded-full" />
        </div>
      </CardHeader>
      <CardContent>
        <div className="flex items-end justify-between gap-4">
          <Skeleton className="h-9 w-24" />
          <Skeleton className="h-4 w-24" />
        </div>
        <Skeleton className="mt-4 h-2 w-full rounded-full" />
        <Skeleton className="mt-3 h-3 w-44" />
      </CardContent>
    </Card>
  )
}
