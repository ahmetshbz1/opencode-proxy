import type { Tone } from './types'

export function quotaTone(remaining?: number): Tone {
  if (remaining === undefined) return 'muted'
  if (remaining <= 20) return 'danger'
  if (remaining <= 50) return 'warning'
  return 'success'
}

export function quotaTextClass(tone: Tone): string {
  if (tone === 'success') return 'text-emerald-300'
  if (tone === 'warning') return 'text-yellow-300'
  if (tone === 'danger') return 'text-red-300'
  return 'text-zinc-100'
}

export function quotaBarClass(tone: Tone): string {
  if (tone === 'success') return 'bg-emerald-400'
  if (tone === 'warning') return 'bg-yellow-400'
  if (tone === 'danger') return 'bg-red-500'
  return 'bg-zinc-500'
}
