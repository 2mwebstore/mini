<script setup lang="ts">
// A square icon button with a tinted background, for row actions (View, Edit,
// Delete…) and paging. The label is its tooltip and accessible name.
type Kind = 'view' | 'edit' | 'delete' | 'password' | 'approve' | 'reject' | 'branches' | 'open'
  | 'first' | 'previous' | 'next' | 'last'
const props = defineProps<{ kind: Kind; label: string; loading?: boolean; disabled?: boolean; to?: string }>()
defineEmits<{ click: [e: MouseEvent] }>()

const icons: Record<Kind, string> = {
  view: 'M2 12s3.6-7 10-7 10 7 10 7-3.6 7-10 7S2 12 2 12zm10 3a3 3 0 1 0 0-6 3 3 0 0 0 0 6z',
  edit: 'M12 20h9M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4z',
  delete: 'M3 6h18M8 6V4h8v2m-9 0 1 14h8l1-14M10 11v6m4-6v6',
  password: 'M15 7a4 4 0 1 1-3.9 4.9L4 19v2h3v-2h2v-2h2l1.1-1.1A4 4 0 0 1 15 7zm1.5 2.5h.01',
  approve: 'M20 6 9 17l-5-5',
  reject: 'M18 6 6 18M6 6l12 12',
  branches: 'M3 21h18M5 21V8l7-5 7 5v13M9 21v-6h6v6',
  open: 'M7 17 17 7M8 7h9v9',
  first: 'M11 17l-5-5 5-5M18 17l-5-5 5-5',
  previous: 'M15 18l-6-6 6-6',
  next: 'M9 18l6-6-6-6',
  last: 'M13 17l5-5-5-5M6 17l5-5-5-5',
}
// Tinted background per meaning; paging buttons stay neutral.
const tone = computed(() => ({
  view: 'bg-info/10 text-info hover:bg-info/20',
  open: 'bg-info/10 text-info hover:bg-info/20',
  edit: 'bg-primary/10 text-primary hover:bg-primary/20',
  branches: 'bg-primary/10 text-primary hover:bg-primary/20',
  password: 'bg-warning/10 text-warning hover:bg-warning/20',
  approve: 'bg-success/10 text-success hover:bg-success/20',
  delete: 'bg-danger/10 text-danger hover:bg-danger/20',
  reject: 'bg-danger/10 text-danger hover:bg-danger/20',
}[props.kind as string] ?? 'bg-surface-2 text-muted hover:text-fg'))
const cls = computed(() => [
  'inline-flex size-8 shrink-0 items-center justify-center rounded-lg transition focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary/40',
  'disabled:cursor-not-allowed disabled:opacity-40',
  tone.value,
])
</script>

<template>
  <NuxtLink v-if="to && !disabled" :to="to" :class="cls" :title="label" :aria-label="label">
    <svg viewBox="0 0 24 24" class="size-4" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path :d="icons[kind]" /></svg>
  </NuxtLink>
  <button v-else type="button" :class="cls" :title="label" :aria-label="label" :disabled="disabled || loading" @click="$emit('click', $event)">
    <span v-if="loading" class="size-3.5 animate-spin rounded-full border-2 border-current border-t-transparent" aria-hidden="true" />
    <svg v-else viewBox="0 0 24 24" class="size-4" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path :d="icons[kind]" /></svg>
  </button>
</template>
