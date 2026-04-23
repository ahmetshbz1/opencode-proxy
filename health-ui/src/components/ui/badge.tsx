import { mergeProps } from '@base-ui/react/merge-props'
import { useRender } from '@base-ui/react/use-render'
import { cva, type VariantProps } from 'class-variance-authority'
import { cn } from '../../lib/utils'

const badgeVariants = cva(
  'relative inline-flex shrink-0 items-center justify-center gap-1 whitespace-nowrap rounded-md border border-transparent font-medium outline-none transition-shadow',
  {
    defaultVariants: { size: 'default', variant: 'outline' },
    variants: {
      size: {
        default: 'h-5.5 min-w-5.5 px-1.5 text-xs',
        lg: 'h-6.5 min-w-6.5 px-2 text-sm',
      },
      variant: {
        outline: 'border-input bg-background text-foreground',
        success: 'bg-success/10 text-success-foreground',
        warning: 'bg-warning/10 text-warning-foreground',
        destructive: 'bg-destructive/10 text-destructive-foreground',
        secondary: 'bg-secondary text-secondary-foreground',
      },
    },
  },
)

interface BadgeProps extends useRender.ComponentProps<'span'> {
  variant?: VariantProps<typeof badgeVariants>['variant']
  size?: VariantProps<typeof badgeVariants>['size']
}

function Badge({ className, variant, size, render, ...props }: BadgeProps) {
  const defaultProps = { className: cn(badgeVariants({ className, size, variant })), 'data-slot': 'badge' }
  return useRender({ defaultTagName: 'span', props: mergeProps<'span'>(defaultProps, props), render })
}

export { Badge, badgeVariants }
