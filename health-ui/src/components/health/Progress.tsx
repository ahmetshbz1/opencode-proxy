import type { Tone } from '../../domain/health/types'
import { cn } from '../../lib/utils'
import { quotaBarClass } from '../../domain/health/quota'

export function Progress({ value, tone, className }: { value: number; tone: Tone; className?: string }) {
  return (
    <div className={cn('h-2 overflow-hidden rounded-full bg-zinc-800', className)}>
      <div className={cn('h-full rounded-full transition-all', quotaBarClass(tone))} style={{ width: `${value}%` }} />
    </div>
  )
}
