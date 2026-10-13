<script setup lang="ts">
const props = withDefaults(
  defineProps<{
    variant?: 'primary' | 'secondary' | 'danger' | 'success' | 'ghost'
    size?: 'sm' | 'md'
    loading?: boolean
    disabled?: boolean
    type?: 'button' | 'submit'
  }>(),
  { variant: 'primary', size: 'md', type: 'button' },
)

const classes = computed(() => [
  'inline-flex items-center justify-center gap-2 rounded-lg font-medium transition focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary/40 disabled:cursor-not-allowed disabled:opacity-50',
  props.size === 'sm' ? 'px-2.5 py-1.5 text-xs' : 'px-3.5 py-2 text-sm',
  {
    primary: 'bg-primary text-primary-fg hover:brightness-110',
    secondary: 'border border-border bg-surface text-fg hover:bg-surface-2',
    danger: 'bg-danger/10 text-danger hover:bg-danger/20',
    success: 'bg-success text-white hover:brightness-110',
    ghost: 'text-muted hover:bg-surface-2 hover:text-fg',
  }[props.variant],
])
</script>

<template>
  <button :type="type" :class="classes" :disabled="disabled || loading">
    <span v-if="loading" class="size-3.5 animate-spin rounded-full border-2 border-current border-t-transparent" aria-hidden="true" />
    <slot />
  </button>
</template>
