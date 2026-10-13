<script setup lang="ts">
import { attendanceService } from '~/services/attendance.service'

// Attendance photos are private: they are streamed by the API after a scope check,
// so they are fetched with the app's auth header rather than via <img src>.
const props = defineProps<{ photoId: number | null; alt: string }>()

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
  <div class="aspect-[3/4] w-full overflow-hidden rounded-xl bg-page">
    <img v-if="url" :src="url" :alt="alt" class="size-full object-cover" />
    <div v-else class="flex size-full items-center justify-center text-sm text-hint">
      {{ failed || !photoId ? 'Photo unavailable' : 'Loading photo…' }}
    </div>
  </div>
</template>
