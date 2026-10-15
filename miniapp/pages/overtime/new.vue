<script setup lang="ts">
import type { ApiError } from '~/services/api'
import { overtimeService } from '~/services/overtime.service'
import { useAuthStore } from '~/stores/auth'
import type { OvertimePlan } from '~/types/api'
import { formatMinutes, todayISO } from '~/utils/format'

// Ask for overtime: a date, from–to (by default from the end of that day's shift,
// for an hour) and what it is for. The line manager approves it.
definePageMeta({ layout: 'plain' })

const router = useRouter()
const auth = useAuthStore()
useBackButton(() => (window.history.state?.back ? router.back() : navigateTo('/overtime', { replace: true })))

const form = reactive({ date: todayISO(auth.timezone), from: '', to: '', reason: '' })
const plan = ref<OvertimePlan | null>(null)
const planError = ref('')
let seq = 0
// A new date suggests that day's hours again: from the shift's end, for an hour.
watch(() => form.date, async (date) => {
  if (!date) return
  const mine = ++seq
  planError.value = ''
  try {
    const p = await overtimeService.plan(date)
    if (mine !== seq) return
    plan.value = p
    form.from = p.default_from
    form.to = p.default_to
  } catch (e) {
    if (mine === seq) planError.value = (e as ApiError).message
  }
}, { immediate: true })

const toMinutes = (t: string) => {
  const [h, m] = t.split(':').map(Number)
  return h! * 60 + m!
}
// The length, and whether it runs past midnight (a "to" at or before "from").
const length = computed(() => {
  if (!form.from || !form.to) return null
  let mins = toMinutes(form.to) - toMinutes(form.from)
  const nextDay = mins <= 0
  if (nextDay) mins += 24 * 60
  return { mins, nextDay }
})
const lengthProblem = computed(() => {
  const l = length.value
  if (!l) return ''
  if (l.mins < 15) return 'At least 15 minutes.'
  if (l.mins > 12 * 60) return 'At most 12 hours.'
  return ''
})

const submitting = ref(false)
const submitError = ref('')
const valid = computed(() => !!length.value && !lengthProblem.value && form.reason.trim().length > 0)

async function submit() {
  if (!valid.value || submitting.value) return
  submitting.value = true
  submitError.value = ''
  try {
    await overtimeService.submit({ date: form.date, start_time: form.from, end_time: form.to, reason: form.reason.trim() })
    haptic('success')
    await navigateTo('/overtime', { replace: true })
  } catch (e) {
    haptic('error')
    submitError.value = (e as ApiError).message
  } finally {
    submitting.value = false
  }
}

const { native } = useMainButton({
  text: 'Submit request',
  visible: () => !!plan.value,
  disabled: () => !valid.value,
  loading: submitting,
  onClick: submit,
})
</script>

<template>
  <div class="flex flex-col gap-4">
    <h1 class="text-2xl font-bold">Request overtime</h1>

    <form class="flex flex-col gap-4" @submit.prevent="submit">
      <AppCard title="When">
        <DateField v-model="form.date" label="Date" required />
        <p v-if="plan" class="mt-3 flex items-center gap-1.5 text-[13px] text-hint">
          <AppIcon :name="plan.cross_day ? 'moon' : 'clock'" class="size-4 shrink-0" />
          <span v-if="plan.work">Your shift: {{ plan.shift_name || 'Schedule' }} {{ plan.shift_start }} – {{ plan.shift_end }}<template v-if="plan.cross_day"> (ends next day)</template></span>
          <span v-else>A day off — your usual shift ends at {{ plan.shift_end }}</span>
        </p>
        <p v-else-if="planError" class="mt-3 text-sm text-danger">{{ planError }}</p>
        <div class="mt-3 grid grid-cols-2 gap-2.5">
          <TimeField v-model="form.from" label="From" required />
          <TimeField v-model="form.to" label="To" :note="length?.nextDay ? 'next day' : undefined" required />
        </div>
        <p class="mt-3 text-sm" :class="lengthProblem ? 'text-danger' : 'text-hint'" aria-live="polite">
          <template v-if="lengthProblem">{{ lengthProblem }}</template>
          <template v-else-if="length"><b class="text-fg">{{ formatMinutes(length.mins) }}</b> of overtime<template v-if="length.nextDay"> · ends the next day</template></template>
        </p>
      </AppCard>

      <AppCard title="Reason">
        <textarea
          v-model="form.reason"
          rows="3"
          maxlength="500"
          required
          aria-label="Reason"
          placeholder="What is the overtime for?"
          class="w-full rounded-xl border border-separator bg-surface p-3 text-[15px] outline-none focus:border-primary"
        />
      </AppCard>

      <p class="px-1 text-[13px] text-hint">Your line manager approves it; you'll get a message in Telegram.</p>
      <p v-if="submitError" class="rounded-2xl bg-danger/10 px-4 py-3 text-[15px] text-danger" role="alert">{{ submitError }}</p>
      <AppButton v-if="!native" type="submit" block :disabled="!valid" :loading="submitting">Submit request</AppButton>
    </form>
  </div>
</template>
