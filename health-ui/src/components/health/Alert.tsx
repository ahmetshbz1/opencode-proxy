import type { ReactNode } from 'react'

export function Alert({ children }: { children: ReactNode }) {
  return <div className="mt-5 rounded-lg border border-yellow-500/60 bg-yellow-950/30 p-3 text-sm text-yellow-100">{children}</div>
}
