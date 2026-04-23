import { mergeProps } from '@base-ui/react/merge-props'
import { useRender } from '@base-ui/react/use-render'
import { cn } from '../../lib/utils'

function Card({ className, render, ...props }: useRender.ComponentProps<'div'>) {
  const defaultProps = {
    className: cn(
      'relative flex flex-col rounded-2xl border bg-card text-card-foreground shadow-xs/5 before:pointer-events-none before:absolute before:inset-0 before:rounded-[calc(var(--radius-2xl)-1px)] before:shadow-[0_-1px_--theme(--color-white/6%)]',
      className,
    ),
    'data-slot': 'card',
  }
  return useRender({ defaultTagName: 'div', props: mergeProps<'div'>(defaultProps, props), render })
}

function CardHeader({ className, render, ...props }: useRender.ComponentProps<'div'>) {
  const defaultProps = { className: cn('grid auto-rows-min gap-1.5 p-6', className), 'data-slot': 'card-header' }
  return useRender({ defaultTagName: 'div', props: mergeProps<'div'>(defaultProps, props), render })
}

function CardTitle({ className, render, ...props }: useRender.ComponentProps<'div'>) {
  const defaultProps = { className: cn('font-semibold text-lg leading-none', className), 'data-slot': 'card-title' }
  return useRender({ defaultTagName: 'div', props: mergeProps<'div'>(defaultProps, props), render })
}

function CardDescription({ className, render, ...props }: useRender.ComponentProps<'div'>) {
  const defaultProps = { className: cn('text-muted-foreground text-sm', className), 'data-slot': 'card-description' }
  return useRender({ defaultTagName: 'div', props: mergeProps<'div'>(defaultProps, props), render })
}

function CardContent({ className, render, ...props }: useRender.ComponentProps<'div'>) {
  const defaultProps = { className: cn('flex-1 p-6 pt-0', className), 'data-slot': 'card-content' }
  return useRender({ defaultTagName: 'div', props: mergeProps<'div'>(defaultProps, props), render })
}

export { Card, CardContent, CardDescription, CardHeader, CardTitle }
