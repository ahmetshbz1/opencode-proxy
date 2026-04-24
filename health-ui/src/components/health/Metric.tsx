import type { ReactNode } from 'react'
import { Card, CardContent, CardHeader } from '../ui/card'
import { Skeleton } from './Skeleton'

export function Metric({ icon, label, value }: { icon: ReactNode; label: string; value: string }) {
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

export function MetricSkeleton() {
  return (
    <Card>
      <CardHeader>
        <div className="flex items-center gap-3">
          <Skeleton className="size-6 rounded-md" />
          <Skeleton className="h-3 w-24" />
        </div>
      </CardHeader>
      <CardContent>
        <Skeleton className="h-9 w-20" />
      </CardContent>
    </Card>
  )
}
