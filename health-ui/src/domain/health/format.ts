export function formatDuration(seconds?: number): string {
  if (!seconds || seconds <= 0) return 'bilinmiyor'
  const hours = Math.floor(seconds / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)
  if (hours > 0) return `${hours}s ${minutes}dk`
  return `${minutes}dk`
}

export function formatCacheAge(seconds?: number): string {
  if (seconds === undefined || seconds <= 0) return 'şimdi'
  if (seconds < 60) return `${seconds}sn önce`
  const minutes = Math.floor(seconds / 60)
  return `${minutes}dk önce`
}
