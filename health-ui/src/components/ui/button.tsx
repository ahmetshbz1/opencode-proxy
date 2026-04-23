import { mergeProps } from '@base-ui/react/merge-props'
import { useRender } from '@base-ui/react/use-render'
import { cva, type VariantProps } from 'class-variance-authority'
import type * as React from 'react'
import { cn } from '../../lib/utils'

const buttonVariants = cva(
  'relative inline-flex shrink-0 cursor-pointer items-center justify-center gap-2 whitespace-nowrap rounded-lg border font-medium outline-none transition-[box-shadow,transform] active:scale-[0.98] before:pointer-events-none before:absolute before:inset-0 before:rounded-[calc(var(--radius-lg)-1px)] focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-1 focus-visible:ring-offset-background disabled:pointer-events-none disabled:opacity-60 [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*=size-])]:size-4',
  {
    defaultVariants: { size: 'default', variant: 'outline' },
    variants: {
      size: {
        default: 'h-9 px-3 text-sm',
        sm: 'h-8 gap-1.5 px-2.5 text-xs',
        lg: 'h-10 px-4 text-base',
        icon: 'size-9',
      },
      variant: {
        default: 'border-primary bg-primary text-primary-foreground shadow-primary/20 shadow-xs hover:bg-primary/90',
        outline: 'border-input bg-popover text-foreground shadow-xs/5 hover:bg-accent/50',
        ghost: 'border-transparent text-foreground hover:bg-accent',
        secondary: 'border-transparent bg-secondary text-secondary-foreground hover:bg-secondary/80',
      },
    },
  },
)

interface ButtonProps extends useRender.ComponentProps<'button'> {
  variant?: VariantProps<typeof buttonVariants>['variant']
  size?: VariantProps<typeof buttonVariants>['size']
}

function Button({ className, variant, size, render, ...props }: ButtonProps) {
  const typeValue: React.ButtonHTMLAttributes<HTMLButtonElement>['type'] = render ? undefined : 'button'
  const defaultProps = {
    className: cn(buttonVariants({ className, size, variant })),
    'data-slot': 'button',
    type: typeValue,
  }
  return useRender({ defaultTagName: 'button', props: mergeProps<'button'>(defaultProps, props), render })
}

export { Button, buttonVariants }
