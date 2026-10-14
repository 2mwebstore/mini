<script setup lang="ts">
import { mapsUrl } from '~/utils/format'

// Where a check-in/out was made: a pin that opens the spot in Google Maps. Green
// inside the office radius, amber outside, grey when the branch has no office set.
const props = defineProps<{ latitude: number; longitude: number; inside?: boolean | null; distance?: number | null; what?: string }>()
const tone = computed(() => (props.inside == null ? 'text-muted' : props.inside ? 'text-success' : 'text-warning'))
const title = computed(() => {
  const where = props.inside == null ? 'Location' : `${props.inside ? 'Inside' : 'Outside'} the office radius${props.distance != null ? ` (${Math.round(props.distance)} m)` : ''}`
  return `${props.what ? `${props.what}: ` : ''}${where} — open in Google Maps`
})
</script>

<template>
  <a
    :href="mapsUrl(latitude, longitude)" target="_blank" rel="noopener noreferrer"
    class="inline-flex rounded p-0.5 transition hover:bg-surface-2 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary/40"
    :class="tone" :title="title" :aria-label="title" @click.stop
  >
    <svg viewBox="0 0 24 24" class="size-3.5" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M12 21s-7-6.2-7-11.5A7 7 0 0 1 19 9.5C19 14.8 12 21 12 21zm0-9a2.5 2.5 0 1 0 0-5 2.5 2.5 0 0 0 0 5z" /></svg>
  </a>
</template>
