export type UsageWindow = {
  used_percent: number
  limit_window_seconds: number
  reset_after_seconds: number
  reset_at: number
  reset_at_formatted?: string
}

export type UsageSnapshot = {
  email?: string
  plan_type?: string
  allowed: boolean
  limit_reached: boolean
  rate_limit_reached_type?: string
  primary_window?: UsageWindow
  secondary_window?: UsageWindow
  fetched_at: string
  cache_age_seconds?: number
}

export type ProviderHealth = {
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

export type HealthResponse = {
  status: string
  port: number
  provider_count: number
  generated_at: string
  providers: ProviderHealth[]
}

export type HealthTab = 'all' | 'codex' | 'limited' | 'ready'
export type Tone = 'success' | 'warning' | 'danger' | 'muted'
