<script setup lang="ts">
import { ApiError } from '~/services/api'
import { attendanceService } from '~/services/attendance.service'
import { useAuthStore } from '~/stores/auth'
import type { SubmitResult, TodayStatus } from '~/types/api'
import { formatClock, formatDate, formatMinutes } from '~/utils/format'

definePageMeta({ layout: 'plain', validate: route => route.params.type === 'in' || route.params.type === 'out' })

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()

const type = computed(() => (route.params.type === 'in' ? 'CHECK_IN' : 'CHECK_OUT'))
const isIn = computed(() => type.value === 'CHECK_IN')
const noun = computed(() => (isIn.value ? 'check-in' : 'check-out'))

const goHome = () => navigateTo('/', { replace: true })
useBackButton(() => (window.history.state?.back ? router.back() : goHome()))

// The server decides whether this is allowed and whether location is needed.
// No photo: a check-in/out is the time and, when the branch asks for it, the location.
const today = ref<TodayStatus | null>(null)
const precheck = computed(() => (today.value ? (isIn.value ? today.value.check_in : today.value.check_out) : null))
const precheckError = ref<ApiError | null>(null)
const location = useLocation()
async function loadPrecheck() {
  precheckError.value = null
  try {
    today.value = await attendanceService.today()
    if (precheck.value?.allowed && precheck.value.need_gps && location.state.value !== 'ok') location.locate()
  } catch (e) {
    precheckError.value = e as ApiError
  }
}
onMounted(loadPrecheck)

const locationReady = computed(() => {
  const pre = precheck.value
  if (!pre?.need_gps) return true
  if (location.state.value === 'ok') return true
  // Optional location: the employee may submit without it once we tried.
  return !pre.gps_required && (location.state.value === 'denied' || location.state.value === 'unavailable')
})

// Live clock: the time that will be recorded.
const now = ref(new Date().toISOString())
let tick: ReturnType<typeof setInterval> | undefined
onMounted(() => (tick = setInterval(() => (now.value = new Date().toISOString()), 1000)))
onBeforeUnmount(() => clearInterval(tick))

const submitting = ref(false)
const problem = ref('')
const result = ref<SubmitResult | null>(null)

async function submit() {
  if (!locationReady.value || submitting.value) return
  submitting.value = true
  problem.value = ''
  try {
    result.value = await attendanceService.submit(type.value, location.coords.value)
    haptic('success')
  } catch (e) {
    haptic('error')
    const err = e as ApiError
    problem.value = err.message
    if (err.code === 'OUTSIDE_OFFICE' || err.code === 'LOCATION_REQUIRED') location.state.value = 'idle'
    if (err.code === 'NOT_ALLOWED' || err.status === 409) loadPrecheck()
  } finally {
    submitting.value = false
  }
}

const label = computed(() => (isIn.value ? 'Check in now' : 'Check out now'))
const { native } = useMainButton({
  text: () => (result.value ? 'Done' : label.value),
  visible: () => !!result.value || !!precheck.value?.allowed,
  disabled: () => !result.value && !locationReady.value,
  loading: submitting,
  onClick: () => (result.value ? goHome() : submit()),
})

const shift = computed(() => {
  const t = today.value
  if (!t) return ''
  if (t.holiday) return `Holiday · ${t.holiday}`
  if (!t.work_day) return 'Day off'
  return `${t.shift_name || 'Schedule'} ${t.schedule_start} – ${t.schedule_end}${t.cross_day ? ' (+1 day)' : ''}`
})
const resultAt = computed(() => {
  const a = result.value?.attendance
  return formatClock(isIn.value ? a?.check_in_at : a?.check_out_at, auth.timezone)
})
const locationText = computed(() => {
  switch (location.state.value) {
    case 'ok': {
      const acc = location.coords.value?.accuracy
      return acc ? `Found (±${Math.round(acc)} m)` : 'Found'
    }
    case 'locating': return 'Finding your location…'
    case 'denied': return 'Location access is off.'
    case 'unavailable': return 'Location unavailable.'
    default: return 'Not shared yet.'
  }
})
</script>

<template>
  <div class="flex flex-col gap-4">
    <h1 class="text-2xl font-bold">{{ isIn ? 'Check in' : 'Check out' }}</h1>

    <!-- result -->
    <div v-if="result" class="flex flex-col items-center gap-2 rounded-2xl bg-surface px-4 py-8 text-center">
      <span class="flex size-16 items-center justify-center rounded-full" :class="isIn ? 'bg-success/15 text-success' : 'bg-primary/15 text-accent'">
        <AppIcon name="check" class="size-8" />
      </span>
      <p class="text-xl font-semibold">{{ isIn ? 'Checked in' : 'Checked out' }} at {{ resultAt }}</p>
      <template v-if="isIn">
        <StatusBadge v-if="result.attendance.late_minutes" tone="warning" :label="`Late ${formatMinutes(result.attendance.late_minutes)}`" />
        <StatusBadge v-else tone="success" label="On time" />
      </template>
      <template v-else>
        <p class="text-hint">Worked {{ formatMinutes(result.attendance.working_minutes) }}</p>
        <StatusBadge v-if="result.attendance.early_leave_minutes" tone="warning" :label="`Left ${formatMinutes(result.attendance.early_leave_minutes)} early`" />
      </template>
      <p v-if="result.location_label !== 'Not provided'" class="flex items-center gap-1 text-sm text-hint">
        <AppIcon name="pin" class="size-4" />{{ result.location_label }}
      </p>
      <p class="mt-2 text-sm text-hint">
        {{ result.pending ? 'Sent for confirmation. You will get a message when it is reviewed.' : 'Confirmed.' }}
      </p>
      <AppButton v-if="!native" class="mt-4" block @click="goHome">Done</AppButton>
    </div>

    <StateView v-else :loading="!today && !precheckError" :error="precheckError" @retry="loadPrecheck">
      <div v-if="precheck && !precheck.allowed" class="rounded-2xl bg-surface px-4 py-6 text-center">
        <p class="text-[15px]">{{ precheck.message }}</p>
        <AppButton class="mt-4" variant="secondary" @click="goHome">Back to home</AppButton>
      </div>

      <template v-else-if="precheck && today">
        <section class="flex flex-col items-center rounded-3xl bg-surface px-4 py-6 text-center" aria-label="Time to record">
          <span class="flex size-14 items-center justify-center rounded-full" :class="isIn ? 'bg-success/15 text-success' : 'bg-primary/15 text-accent'">
            <AppIcon :name="isIn ? 'in' : 'out'" class="size-7" />
          </span>
          <p class="mt-3 text-[13px] text-hint">{{ formatDate(today.date, 'long') }}</p>
          <p class="text-[44px] font-bold leading-none tracking-tight tabular-nums" aria-live="off">{{ formatClock(now, auth.timezone) }}</p>
          <p class="mt-3 inline-flex max-w-full items-center gap-1.5 rounded-full bg-page px-3 py-1.5 text-[13px] text-hint">
            <AppIcon :name="today.cross_day ? 'moon' : 'clock'" class="size-4 shrink-0" />
            <span class="truncate">{{ shift }}</span>
          </p>
          <p v-if="!isIn && today.attendance?.check_in_at" class="mt-2 text-[13px] text-hint">
            Checked in at {{ formatClock(today.attendance.check_in_at, auth.timezone) }}
          </p>
        </section>

        <div v-if="precheck.need_gps" class="flex items-center justify-between gap-3 rounded-2xl bg-surface px-4 py-3">
          <div class="flex min-w-0 items-center gap-3">
            <span class="flex size-9 shrink-0 items-center justify-center rounded-xl" :class="location.state.value === 'ok' ? 'bg-success/15 text-success' : 'bg-page text-hint'">
              <AppIcon name="pin" class="size-5" />
            </span>
            <div class="min-w-0 text-[15px]">
              <p class="font-medium">Location{{ precheck.gps_required ? '' : ' (optional)' }}</p>
              <p class="text-[13px] text-hint" aria-live="polite">{{ locationText }}</p>
            </div>
          </div>
          <span v-if="location.state.value === 'locating'" class="size-5 shrink-0 animate-spin rounded-full border-2 border-hint border-t-transparent" aria-hidden="true" />
          <AppButton
            v-else-if="location.state.value === 'denied' && location.canOpenSettings.value"
            variant="secondary"
            @click="location.openSettings()"
          >Settings</AppButton>
          <AppButton
            v-else-if="location.state.value !== 'ok'"
            variant="secondary"
            @click="location.locate()"
          >{{ location.state.value === 'idle' ? 'Share' : 'Retry' }}</AppButton>
        </div>

        <p v-if="problem" class="rounded-2xl bg-danger/10 px-4 py-3 text-[15px] text-danger" role="alert">{{ problem }}</p>

        <AppButton v-if="!native" block :variant="isIn ? 'success' : 'primary'" class="py-4 text-[17px]" :disabled="!locationReady" :loading="submitting" @click="submit">
          {{ label }}
        </AppButton>
        <p v-else-if="!locationReady" class="text-center text-[13px] text-hint">Share your location to {{ noun.replace('-', ' ') }}.</p>
      </template>
    </StateView>
  </div>
</template>
