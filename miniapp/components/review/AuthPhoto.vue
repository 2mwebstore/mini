<script setup lang="ts">
import { attendanceService } from '~/services/attendance.service'

// Attendance photos are private: they are streamed by the API after a scope check,
// so they are fetched with the app's auth header rather than via <img src>.
// zoomable: tapping the photo shows it full screen (reviewers checking a face).
const props = defineProps<{ photoId: number | null; alt: string; zoomable?: boolean }>()
const zoomed = ref(false)

const url = ref<string>()
const failed = ref(false)
let ctrl: AbortController | undefined

watch(
  () => props.photoId,
  async id => {
    ctrl?.abort()
    if (url.value) URL.revokeObjectURL(url.value)
    url.value = undefined
    failed.value = false
    if (!id) return
    ctrl = new AbortController()
    try {
      url.value = URL.createObjectURL(await attendanceService.photo(id, ctrl.signal))
    } catch (e) {
      if ((e as Error).name !== 'AbortError') failed.value = true
    }
  },
  { immediate: true },
)

onBeforeUnmount(() => {
  ctrl?.abort()
  if (url.value) URL.revokeObjectURL(url.value)
})
</script>

<template>
  <div class="aspect-[3/4] overflow-hidden rounded-xl bg-page">
    <button v-if="url && zoomable" type="button" class="size-full" :aria-label="`View ${alt} full screen`" @click="zoomed = true">
      <img :src="url" :alt="alt" class="size-full object-cover" />
    </button>
    <img v-else-if="url" :src="url" :alt="alt" class="size-full object-cover" />
    <div v-else class="flex size-full items-center justify-center p-1 text-center text-xs text-hint">
      {{ failed || !photoId ? 'Photo unavailable' : 'Loading…' }}
    </div>
  </div>
  <Teleport to="body">
    <div
      v-if="zoomed && url"
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/90 p-4"
      role="dialog"
      aria-modal="true"
      :aria-label="alt"
      @click="zoomed = false"
    >
      <img :src="url" :alt="alt" class="size-full object-contain" />
      <button
        type="button"
        class="absolute right-4 top-[max(1rem,env(safe-area-inset-top))] flex size-10 items-center justify-center rounded-full bg-white/15 text-white"
        aria-label="Close photo"
      >
        <AppIcon name="close" class="size-5" />
      </button>
    </div>
  </Teleport>
</template>
