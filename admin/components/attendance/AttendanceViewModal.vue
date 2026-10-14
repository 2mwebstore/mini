<script setup lang="ts">
// Quick view of one attendance day from the list, with a link to its own page.
import { ApiError } from '~/services/api'
import { attendanceService } from '~/services/attendance.service'
import { useAuthStore } from '~/stores/auth'
import type { AttendanceDetail } from '~/types/api'
import { formatDate } from '~/utils/format'

const props = defineProps<{ id: number | null }>()
const emit = defineEmits<{ close: []; changed: []; edit: [id: number] }>()

const auth = useAuthStore()
const open = computed({ get: () => props.id !== null, set: v => !v && emit('close') })
const detail = ref<AttendanceDetail | null>(null)
const error = ref<ApiError | null>(null)
const loading = ref(false)
async function load() {
  if (props.id === null) return
  loading.value = true
  error.value = null
  try {
    detail.value = await attendanceService.get(props.id)
  } catch (e) {
    error.value = e instanceof ApiError ? e : new ApiError(0, 'UNKNOWN', 'Something went wrong. Please try again.')
  } finally {
    loading.value = false
  }
}
watch(() => props.id, id => {
  detail.value = null
  if (id !== null) load()
}, { immediate: true })

const title = computed(() => {
  const a = detail.value?.attendance
  return a ? `${a.employee?.full_name ?? 'Attendance'} · ${formatDate(a.work_date)}` : 'Attendance'
})
function changed() {
  load()
  emit('changed')
}
</script>

<template>
  <AppModal v-model="open" :title="title" xwide>
    <StateView :loading="loading && !detail" :error="error" @retry="load">
      <AttendanceDetailView v-if="detail" :detail="detail" @changed="changed" />
    </StateView>
    <template #footer>
      <NuxtLink v-if="id !== null" :to="`/attendance/${id}`" class="mr-auto self-center text-sm text-primary hover:underline">Open full page</NuxtLink>
      <AppButton v-if="auth.can('attendance.edit') && detail" variant="secondary" @click="emit('edit', id!)">Edit</AppButton>
      <AppButton @click="open = false">Close</AppButton>
    </template>
  </AppModal>
</template>
