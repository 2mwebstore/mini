<script setup lang="ts">
import { ApiError } from '~/services/api'
import { attendanceService } from '~/services/attendance.service'
import { useAuthStore } from '~/stores/auth'
import type { Precheck, SubmitResult } from '~/types/api'
import { formatClock, formatMinutes } from '~/utils/format'

definePageMeta({ layout: 'plain', validate: route => route.params.type === 'in' || route.params.type === 'out' })

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()

const type = computed(() => (route.params.type === 'in' ? 'CHECK_IN' : 'CHECK_OUT'))
const noun = computed(() => (type.value === 'CHECK_IN' ? 'check-in' : 'check-out'))

const goHome = () => navigateTo('/', { replace: true })
useBackButton(() => (window.history.state?.back ? router.back() : goHome()))

// The server decides whether this is allowed and whether location is needed.
const precheck = ref<Precheck | null>(null)
const precheckError = ref<ApiError | null>(null)
async function loadPrecheck() {
  precheckError.value = null
  try {
    const t = await attendanceService.today()
    precheck.value = type.value === 'CHECK_IN' ? t.check_in : t.check_out
  } catch (e) {
    precheckError.value = e as ApiError
  }
}
onMounted(loadPrecheck)

const photo = ref<Blob | null>(null)
const location = useLocation()
watch(photo, p => {
  if (p && precheck.value?.need_gps && location.state.value !== 'ok') location.locate()
})

const locationReady = computed(() => {
  const pre = precheck.value
  if (!pre?.need_gps) return true
  if (location.state.value === 'ok') return true
  // Optional location: the employee may submit without it once we tried.
  return !pre.gps_required && (location.state.value === 'denied' || location.state.value === 'unavailable')
})

const submitting = ref(false)
const problem = ref('')
const result = ref<SubmitResult | null>(null)

async function submit() {
  if (!photo.value || !locationReady.value || submitting.value) return
  submitting.value = true
  problem.value = ''
  try {
    result.value = await attendanceService.submit(type.value, photo.value, location.coords.value)
    haptic('success')
  } catch (e) {
    haptic('error')
    const err = e as ApiError
    problem.value = err.message
    if (['PHOTO_REUSED', 'INVALID_PHOTO', 'PHOTO_TOO_LARGE'].includes(err.code)) photo.value = null
    if (err.code === 'OUTSIDE_OFFICE' || err.code === 'LOCATION_REQUIRED') location.state.value = 'idle'
    if (err.code === 'NOT_ALLOWED' || err.status === 409) loadPrecheck()
  } finally {
    submitting.value = false
  }
}

const canSubmit = computed(() => !!photo.value && locationReady.value)
const { native } = useMainButton({
  text: () => (result.value ? 'Done' : `Submit ${noun.value}`),
  visible: () => !!result.value || (!!precheck.value?.allowed && !!photo.value),
  disabled: () => !result.value && !canSubmit.value,
  loading: submitting,
  onClick: () => (result.value ? goHome() : submit()),
})

const resultAt = computed(() => {
  const a = result.value?.attendance
  return formatClock(type.value === 'CHECK_IN' ? a?.check_in_at : a?.check_out_at, auth.timezone)
})
</script>

<template>
  <div class="flex flex-col gap-4">
    <h1 class="text-2xl font-bold">{{ type === 'CHECK_IN' ? 'Check in' : 'Check out' }}</h1>

    <!-- result -->
    <div v-if="result" class="flex flex-col items-center gap-2 rounded-2xl bg-surface px-4 py-8 text-center">
      <span class="text-5xl" aria-hidden="true">{{ type === 'CHECK_IN' ? '🟢' : '🔴' }}</span>
      <p class="text-xl font-semibold">{{ type === 'CHECK_IN' ? 'Checked in' : 'Checked out' }} at {{ resultAt }}</p>
      <template v-if="type === 'CHECK_IN'">
        <StatusBadge v-if="result.attendance.late_minutes" tone="warning" :label="`Late ${formatMinutes(result.attendance.late_minutes)}`" />
        <StatusBadge v-else tone="success" label="On time" />
      </template>
      <template v-else>
        <p class="text-hint">Worked {{ formatMinutes(result.attendance.working_minutes) }}</p>
        <StatusBadge v-if="result.attendance.early_leave_minutes" tone="warning" :label="`Left ${formatMinutes(result.attendance.early_leave_minutes)} early`" />
      </template>
      <p v-if="result.location_label !== 'Not provided'" class="text-sm text-hint">📍 {{ result.location_label }}</p>
      <p class="mt-2 text-sm text-hint">
        {{ result.pending ? 'Sent for confirmation. You will get a message when it is reviewed.' : 'Confirmed.' }}
      </p>
      <AppButton v-if="!native" class="mt-4" block @click="goHome">Done</AppButton>
    </div>

    <StateView v-else :loading="!precheck && !precheckError" :error="precheckError" @retry="loadPrecheck">
      <div v-if="precheck && !precheck.allowed" class="rounded-2xl bg-surface px-4 py-6 text-center">
        <p class="text-[15px]">{{ precheck.message }}</p>
        <AppButton class="mt-4" variant="secondary" @click="goHome">Back to home</AppButton>
      </div>

      <template v-else-if="precheck">
        <CameraCapture v-model="photo" />

        <div v-if="precheck.need_gps" class="flex items-center justify-between gap-3 rounded-2xl bg-surface px-4 py-3">
          <div class="min-w-0 text-[15px]">
            <p class="font-medium">📍 Location {{ precheck.gps_required ? '' : '(optional)' }}</p>
            <p class="text-[13px] text-hint">
              <template v-if="location.state.value === 'ok'">
                Received<template v-if="location.coords.value?.accuracy"> (±{{ Math.round(location.coords.value.accuracy) }} m)</template>
              </template>
              <template v-else-if="location.state.value === 'locating'">Finding your location…</template>
              <template v-else-if="location.state.value === 'denied'">Location access is off.</template>
              <template v-else-if="location.state.value === 'unavailable'">Location unavailable.</template>
              <template v-else>Shared after you take the photo.</template>
            </p>
          </div>
          <AppButton
            v-if="location.state.value === 'denied' && location.canOpenSettings.value"
            variant="secondary"
            @click="location.openSettings()"
          >Settings</AppButton>
          <AppButton
            v-else-if="location.state.value !== 'ok' && location.state.value !== 'locating'"
            variant="secondary"
            @click="location.locate()"
          >{{ location.state.value === 'idle' ? 'Share' : 'Retry' }}</AppButton>
        </div>

        <p v-if="problem" class="rounded-2xl bg-danger/10 px-4 py-3 text-[15px] text-danger" role="alert">{{ problem }}</p>

        <AppButton v-if="!native" block :disabled="!canSubmit" :loading="submitting" @click="submit">Submit {{ noun }}</AppButton>
      </template>
    </StateView>
  </div>
</template>
