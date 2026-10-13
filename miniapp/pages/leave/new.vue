<script setup lang="ts">
import type { ApiError } from '~/services/api'
import { leaveService } from '~/services/leave.service'
import { useAuthStore } from '~/stores/auth'
import type { LeavePreview } from '~/types/api'
import { todayISO } from '~/utils/format'

definePageMeta({ layout: 'plain' })

const router = useRouter()
const auth = useAuthStore()
useBackButton(() => (window.history.state?.back ? router.back() : navigateTo('/leave', { replace: true })))

const { data: types, error: typesError, loading: typesLoading, load: loadTypes } = useResource(() => leaveService.types())

const today = todayISO(auth.timezone)
const form = reactive({ leaveTypeId: 0, start: today, end: today, reason: '' })
watch(types, t => {
  if (t?.length && !form.leaveTypeId) form.leaveTypeId = t[0]!.id
})
watch(
  () => form.start,
  s => {
    if (form.end < s) form.end = s
  },
)

// Working days + server-side checks (overlap, range), refreshed as dates change.
const preview = ref<LeavePreview | null>(null)
const previewError = ref('')
let timer: ReturnType<typeof setTimeout> | undefined
let seq = 0
watch(
  () => [form.leaveTypeId, form.start, form.end] as const,
  ([typeId, start, end]) => {
    clearTimeout(timer)
    preview.value = null
    previewError.value = ''
    if (!typeId || !start || !end) return
    const mine = ++seq
    timer = setTimeout(async () => {
      try {
        const p = await leaveService.preview({ leave_type_id: typeId, start_date: start, end_date: end })
        if (mine === seq) preview.value = p
      } catch (e) {
        if (mine === seq) previewError.value = (e as ApiError).message
      }
    }, 300)
  },
)
onBeforeUnmount(() => clearTimeout(timer))

const submitting = ref(false)
const submitError = ref('')
const valid = computed(() => !!preview.value && form.reason.trim().length > 0)

async function submit() {
  if (!valid.value || submitting.value) return
  submitting.value = true
  submitError.value = ''
  try {
    await leaveService.submit({ leave_type_id: form.leaveTypeId, start_date: form.start, end_date: form.end, reason: form.reason.trim() })
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
        <AppCard title="Type">
          <div class="flex flex-wrap gap-2" role="radiogroup" aria-label="Leave type">
            <button
              v-for="t in types"
              :key="t.id"
              type="button"
              role="radio"
              :aria-checked="form.leaveTypeId === t.id"
              class="rounded-full px-3.5 py-1.5 text-sm font-medium transition"
              :class="form.leaveTypeId === t.id ? 'bg-primary text-primary-fg' : 'bg-page text-fg'"
              @click="form.leaveTypeId = t.id"
            >{{ t.name }}</button>
          </div>
        </AppCard>

        <AppCard title="Dates">
          <div class="grid grid-cols-2 gap-3">
            <label class="text-[13px] text-hint">
              From
              <input v-model="form.start" type="date" required class="mt-1 w-full rounded-xl border border-separator px-3 py-2.5 text-[15px] text-fg" />
            </label>
            <label class="text-[13px] text-hint">
              To
              <input v-model="form.end" type="date" required :min="form.start" class="mt-1 w-full rounded-xl border border-separator px-3 py-2.5 text-[15px] text-fg" />
            </label>
          </div>
          <p class="mt-3 text-sm" :class="previewError ? 'text-danger' : 'text-hint'" aria-live="polite">
            <template v-if="previewError">{{ previewError }}</template>
            <template v-else-if="preview">{{ preview.days }} working day{{ preview.days === 1 ? '' : 's' }}</template>
            <template v-else>Checking dates…</template>
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
            class="w-full rounded-xl border border-separator p-3 text-[15px] outline-none focus:border-primary"
          />
        </AppCard>

        <p v-if="submitError" class="rounded-2xl bg-danger/10 px-4 py-3 text-[15px] text-danger" role="alert">{{ submitError }}</p>
        <AppButton v-if="!native" type="submit" block :disabled="!valid" :loading="submitting">Submit request</AppButton>
      </form>
    </StateView>
  </div>
</template>
