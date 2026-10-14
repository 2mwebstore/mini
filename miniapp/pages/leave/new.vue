<script setup lang="ts">
import type { PickOption } from '~/components/ui/SearchSelect.vue'
import type { ApiError } from '~/services/api'
import { leaveService } from '~/services/leave.service'
import { useAuthStore } from '~/stores/auth'
import type { DayPlan, HalfDay, LeaveBalance, LeavePreview } from '~/types/api'
import { fmtDays, halfLabels, todayISO } from '~/utils/format'

definePageMeta({ layout: 'plain' })

const router = useRouter()
const auth = useAuthStore()
useBackButton(() => (window.history.state?.back ? router.back() : navigateTo('/leave', { replace: true })))

const { data: types, error: typesError, loading: typesLoading, load: loadTypes } = useResource(() => leaveService.types())
// Balances are a nice-to-have on this form: if they fail to load, the picker just shows names.
const balances = ref<Map<number, LeaveBalance>>(new Map())
onMounted(async () => {
  try {
    balances.value = new Map((await leaveService.balances()).filter(b => b.tracked).map(b => [b.leave_type_id, b]))
  } catch {}
})

const today = todayISO(auth.timezone)
const form = reactive({ leaveTypeId: '', whole: 'FULL' as 'FULL' | 'HALF', half: 'FIRST_HALF' as HalfDay, start: today, end: today, reason: '' })
const chosenType = computed(() => types.value?.find(t => String(t.id) === form.leaveTypeId))
const typeOptions = computed((): PickOption[] => (types.value ?? []).map(t => {
  const b = balances.value.get(t.id)
  return {
    value: String(t.id),
    label: t.name,
    sub: t.approval === 'AUTO' ? 'Approved automatically' : t.description || undefined,
    color: t.color,
    hint: b ? `${fmtDays(Math.max(0, b.available))} left` : undefined,
    hintTone: b && b.available <= 0 ? 'warning' : 'hint',
  }
}))
watch(types, t => {
  if (t?.length && !form.leaveTypeId) form.leaveTypeId = String(t[0]!.id)
})
// A half day is one date; a full-day range keeps its end after its start.
watch(() => [form.start, form.whole] as const, ([s, whole]) => {
  if (whole === 'HALF' || form.end < s) form.end = s
})
const dayType = computed(() => (form.whole === 'HALF' ? form.half : 'FULL'))

// Working days, the balance and the shift on the first date (what a half day
// means for this person: a morning/afternoon, or the halves of a night shift),
// refreshed as the form changes. A refusal comes back as preview.problem.
const preview = ref<LeavePreview | null>(null)
const plan = ref<DayPlan | null>(null) // kept while the next preview loads
const previewError = ref('')
let timer: ReturnType<typeof setTimeout> | undefined
let seq = 0
watch(
  () => [form.leaveTypeId, dayType.value, form.start, form.end] as const,
  ([typeId, dt, start, end]) => {
    clearTimeout(timer)
    preview.value = null
    previewError.value = ''
    if (!typeId || !start || !end) return
    const mine = ++seq
    timer = setTimeout(async () => {
      try {
        const p = await leaveService.preview({ leave_type_id: Number(typeId), start_date: start, end_date: end, day_type: dt })
        if (mine !== seq) return
        preview.value = p
        plan.value = p.plan
      } catch (e) {
        if (mine === seq) previewError.value = (e as ApiError).message
      }
    }, 300)
  },
  { immediate: true },
)
onBeforeUnmount(() => clearTimeout(timer))

const halves = computed(() => {
  const p = plan.value
  if (!p?.work) return null
  const names = halfLabels(p)
  return [
    { value: 'FIRST_HALF' as const, ...names[0], from: p.first_half[0], to: p.first_half[1], next: false },
    { value: 'SECOND_HALF' as const, ...names[1], from: p.second_half[0], to: p.second_half[1], next: p.second_half_next_day },
  ]
})
const problem = computed(() => previewError.value || preview.value?.problem?.message || '')

const submitting = ref(false)
const submitError = ref('')
const valid = computed(() => !!preview.value && !preview.value.problem && form.reason.trim().length > 0)

async function submit() {
  if (!valid.value || submitting.value) return
  submitting.value = true
  submitError.value = ''
  try {
    await leaveService.submit({ leave_type_id: Number(form.leaveTypeId), start_date: form.start, end_date: form.end, day_type: dayType.value, reason: form.reason.trim() })
    haptic('success')
    await navigateTo('/leave', { replace: true })
  } catch (e) {
    haptic('error')
    submitError.value = (e as ApiError).message
  } finally {
    submitting.value = false
  }
}

const { native } = useMainButton({
  text: 'Submit request',
  visible: () => !!types.value?.length,
  disabled: () => !valid.value,
  loading: submitting,
  onClick: submit,
})
</script>

<template>
  <div class="flex flex-col gap-4">
    <h1 class="text-2xl font-bold">Request leave</h1>

    <StateView :loading="typesLoading && !types" :error="typesError" :empty="types?.length === 0" empty-text="No leave types are set up yet." @retry="loadTypes">
      <form class="flex flex-col gap-4" @submit.prevent="submit">
        <AppCard title="Leave type">
          <SearchSelect v-model="form.leaveTypeId" :options="typeOptions" title="Leave type" placeholder="Choose a leave type" />
          <p v-if="chosenType && (chosenType.max_days_per_request || chosenType.min_notice_days)" class="mt-2 text-[13px] text-hint">
            <template v-if="chosenType.max_days_per_request">Up to {{ fmtDays(chosenType.max_days_per_request) }} per request.</template>
            <template v-if="chosenType.min_notice_days"> Ask {{ fmtDays(chosenType.min_notice_days) }} ahead.</template>
          </p>
        </AppCard>

        <AppCard title="When">
          <Segmented v-model="form.whole" :options="[{ value: 'FULL', label: 'Full day' }, { value: 'HALF', label: 'Half day' }]" />

          <div v-if="form.whole === 'FULL'" class="mt-3 grid grid-cols-2 gap-2.5">
            <DateField v-model="form.start" label="From" required />
            <DateField v-model="form.end" label="To" :min="form.start" required />
          </div>
          <template v-else>
            <DateField v-model="form.start" label="Date" class="mt-3" required />
            <div v-if="halves" class="mt-3 grid grid-cols-2 gap-2" role="radiogroup" aria-label="Which half">
              <button
                v-for="h in halves"
                :key="h.value"
                type="button"
                role="radio"
                :aria-checked="form.half === h.value"
                class="flex flex-col items-start gap-0.5 rounded-2xl border-2 px-3 py-2.5 text-left transition"
                :class="form.half === h.value ? 'border-primary bg-primary/10' : 'border-separator'"
                @click="form.half = h.value"
              >
                <span class="flex items-center gap-1.5 text-[15px] font-semibold">
                  <AppIcon :name="h.icon" class="size-4" :class="form.half === h.value ? 'text-accent' : 'text-hint'" />{{ h.label }}
                </span>
                <span class="text-[13px] tabular-nums text-hint">{{ h.from }} – {{ h.to }}<span v-if="h.next" class="block">next day</span></span>
              </button>
            </div>
          </template>

          <!-- the person's shift that day: normal or cross-day -->
          <p v-if="plan?.work && (form.whole === 'HALF' || form.start === form.end)" class="mt-3 flex items-center gap-1.5 text-[13px] text-hint">
            <AppIcon :name="plan.work_type === 'CROSS_DAY' ? 'moon' : 'clock'" class="size-4 shrink-0" />
            <span>
              Your shift: {{ plan.shift_name || 'Schedule' }} {{ plan.start }} – {{ plan.end }}<template v-if="plan.work_type === 'CROSS_DAY'"> (ends next day)</template>
            </span>
          </p>
          <p v-else-if="plan?.cross_days && form.whole === 'FULL'" class="mt-3 flex items-center gap-1.5 text-[13px] text-hint">
            <AppIcon name="moon" class="size-4 shrink-0" />
            <span>Night shifts count on the date they start.</span>
          </p>

          <p class="mt-3 text-sm" :class="problem ? 'text-danger' : 'text-hint'" aria-live="polite">
            <template v-if="problem">{{ problem }}</template>
            <template v-else-if="preview">
              <template v-if="form.whole === 'HALF'">Half a working day</template>
              <template v-else>{{ fmtDays(preview.days, 'working day') }}</template>
              <template v-if="preview.balance?.tracked"> · {{ fmtDays(Math.max(0, preview.balance.available - preview.days)) }} left after this</template>
            </template>
            <template v-else>Checking…</template>
          </p>
        </AppCard>

        <AppCard title="Reason">
          <textarea
            v-model="form.reason"
            rows="4"
            maxlength="1000"
            required
            aria-label="Reason"
            placeholder="Why do you need leave?"
            class="w-full rounded-xl border border-separator bg-surface p-3 text-[15px] outline-none focus:border-primary"
          />
        </AppCard>

        <p v-if="submitError" class="rounded-2xl bg-danger/10 px-4 py-3 text-[15px] text-danger" role="alert">{{ submitError }}</p>
        <AppButton v-if="!native" type="submit" block :disabled="!valid" :loading="submitting">Submit request</AppButton>
      </form>
    </StateView>
  </div>
</template>
