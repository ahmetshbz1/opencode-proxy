import { RefreshCcw } from 'lucide-react'
import type { HealthTab } from '../../domain/health/types'
import { Button } from '../ui/button'

const HEALTH_TABS: HealthTab[] = ['all', 'codex', 'limited', 'ready']

export function HealthTabs({ value, onValueChange, onRefresh }: { value: HealthTab; onValueChange: (tab: HealthTab) => void; onRefresh: () => void }) {
  return (
    <nav className="mt-8 flex flex-wrap gap-2 border-b border-zinc-800 pb-4">
      {HEALTH_TABS.map((item) => (
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

function tabTitle(tab: HealthTab): string {
  if (tab === 'all') return 'Tümü'
  if (tab === 'codex') return 'Codex'
  if (tab === 'limited') return 'Limitli'
  return 'Hazır'
}
