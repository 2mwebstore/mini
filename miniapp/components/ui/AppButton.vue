<script setup lang="ts">
const props = withDefaults(
  defineProps<{
    variant?: 'primary' | 'secondary' | 'danger' | 'success' | 'ghost'
    loading?: boolean
    disabled?: boolean
    block?: boolean
    type?: 'button' | 'submit'
  }>(),
  { variant: 'primary', type: 'button' },
)

const classes = computed(() => [
  'inline-flex items-center justify-center gap-2 rounded-xl px-4 py-3 text-[15px] font-semibold transition active:scale-[0.98] disabled:opacity-50 disabled:active:scale-100',
  props.block && 'w-full',
  {
    primary: 'bg-primary text-primary-fg',
    secondary: 'bg-primary/10 text-accent',
    danger: 'bg-danger/10 text-danger',
    success: 'bg-success text-white',
    ghost: 'text-link',
  }[props.variant],
])
</script>

<template>
  <button :type="type" :class="classes" :disabled="disabled || loading">
    <span v-if="loading" class="size-4 animate-spin rounded-full border-2 border-current border-t-transparent" aria-hidden="true" />
    <slot />
  </button>
</template>
