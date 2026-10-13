<script setup lang="ts">
import { attendanceService } from '~/services/attendance.service'
import { reviewService } from '~/services/review.service'
import { useAuthStore } from '~/stores/auth'

const auth = useAuthStore()
const { data: today, error, loading, load } = useResource(() => attendanceService.today())

// Reviewers see how much is waiting for them; a failure here just hides the banner.
const pending = ref<{ attendance: number; leave: number } | null>(null)
onMounted(async () => {
  if (!auth.isReviewer) return
  try {
    const [a, l] = await Promise.all([reviewService.pendingAttendance(), reviewService.pendingLeave()])
    pending.value = { attendance: a.length, leave: l.length }
  } catch {
    pending.value = null
  }
})

const greeting = computed(() => {
  const hour = Number(new Intl.DateTimeFormat('en-GB', { hour: 'numeric', hour12: false, timeZone: auth.timezone }).format(new Date()))
  const part = hour < 12 ? 'Good morning' : hour < 18 ? 'Good afternoon' : 'Good evening'
  return `${part}, ${auth.me?.full_name.split(' ')[0] ?? ''}`
})

// What the big button does right now — the API's precheck decides, so the app
// never offers an action the server would refuse.
const action = computed(() => {
  const t = today.value
  if (!t) return null
  if (t.check_in.allowed) return { to: '/check/in', label: t.attendance?.check_in_at ? 'Submit check-in again' : 'Check in', variant: 'success' as const }
  if (t.check_out.allowed) return { to: '/check/out', label: t.attendance?.check_out_at ? 'Submit check-out again' : 'Check out', variant: 'primary' as const }
  return null
})

const blockedMessage = computed(() => {
  const t = today.value
  if (!t || action.value) return ''
  return (t.attendance?.check_in_at ? t.check_out.message : t.check_in.message) ?? ''
})
</script>

<template>
  <div class="flex flex-col gap-4">
    <header>
      <p class="text-[13px] text-hint">{{ [auth.settings?.company_name, auth.me?.branch_name].filter(Boolean).join(' · ') }}</p>
      <h1 class="text-2xl font-bold">{{ greeting }}</h1>
    </header>

    <StateView :loading="loading && !today" :error="today ? null : error" @retry="load">
      <template v-if="today">
        <TodayCard :today="today" :timezone="auth.timezone" />
        <AppButton v-if="action" block :variant="action.variant" class="py-4 text-[17px]" @click="navigateTo(action.to)">
          {{ action.label }}
        </AppButton>
        <p v-else-if="blockedMessage" class="rounded-2xl bg-surface px-4 py-3 text-center text-[15px] text-hint">
          {{ blockedMessage }}
        </p>
      </template>
    </StateView>

    <NuxtLink
      v-if="pending && pending.attendance + pending.leave > 0"
      to="/team"
      class="flex items-center justify-between rounded-2xl bg-warning/15 px-4 py-3"
    >
      <span class="text-[15px]">
        <b>{{ pending.attendance + pending.leave }}</b> waiting for your review
        <span class="text-hint">· {{ pending.attendance }} attendance · {{ pending.leave }} leave</span>
      </span>
      <span class="text-warning" aria-hidden="true">›</span>
    </NuxtLink>

    <div class="grid grid-cols-2 gap-3">
      <NuxtLink to="/leave/new" class="rounded-2xl bg-surface p-4">
        <span class="text-2xl" aria-hidden="true">🏖️</span>
        <p class="mt-2 font-medium">Request leave</p>
      </NuxtLink>
      <NuxtLink to="/history" class="rounded-2xl bg-surface p-4">
        <span class="text-2xl" aria-hidden="true">📊</span>
        <p class="mt-2 font-medium">My attendance</p>
      </NuxtLink>
    </div>
  </div>
</template>
