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


const initial = computed(() => (auth.me?.full_name ?? '?').trim().charAt(0).toUpperCase())

const shortcuts = [
  { to: '/leave/new', icon: 'calendar-plus', tint: 'bg-success/15 text-success', title: 'Request leave', sub: 'Annual, sick, personal…' },
  { to: '/history', icon: 'chart', tint: 'bg-primary/15 text-accent', title: 'My attendance', sub: 'Days, hours and late arrivals' },
  { to: '/leave', icon: 'leave', tint: 'bg-info/15 text-info', title: 'My leave', sub: 'Status of your requests' },
  { to: '/overtime', icon: 'timer', tint: 'bg-warning/15 text-warning', title: 'Overtime', sub: 'Ask for extra hours after your shift' },
] as const

// Check-in and check-out each have their own tile; the API's precheck decides which
// is available, so the app never offers an action the server would refuse. When
// neither is (on leave, recorded by an administrator…) and the day isn't done, say why.
const blockedMessage = computed(() => {
  const t = today.value
  if (!t || t.check_in.allowed || t.check_out.allowed || t.attendance?.check_out_at) return ''
  return (t.attendance?.check_in_at ? t.check_out.message : t.check_in.message) ?? ''
})
</script>

<template>
  <div class="flex flex-col gap-4">
    <header class="flex items-center gap-3">
      <span class="flex size-11 shrink-0 items-center justify-center rounded-full bg-primary text-lg font-semibold text-primary-fg" aria-hidden="true">
        {{ initial }}
      </span>
      <div class="min-w-0">
        <p class="truncate text-[13px] text-hint">{{ [auth.settings?.company_name, auth.me?.branch_name].filter(Boolean).join(' · ') }}</p>
        <h1 class="truncate text-xl font-bold">{{ greeting }}</h1>
      </div>
    </header>

    <StateView :loading="loading && !today" :error="today ? null : error" @retry="load">
      <TodayCard v-if="today" :today="today" :timezone="auth.timezone">
        <div class="grid grid-cols-2 gap-3">
          <CheckTile kind="in" :today="today" :timezone="auth.timezone" @go="navigateTo('/check/in')" />
          <CheckTile kind="out" :today="today" :timezone="auth.timezone" @go="navigateTo('/check/out')" />
        </div>
        <p v-if="blockedMessage" class="mt-3 rounded-2xl bg-page px-4 py-3 text-center text-[15px] text-hint">
          {{ blockedMessage }}
        </p>
      </TodayCard>
    </StateView>

    <NuxtLink
      v-if="pending && pending.attendance + pending.leave > 0"
      to="/team"
      class="flex items-center gap-3 rounded-2xl bg-surface p-4 active:opacity-70"
    >
      <span class="flex size-10 shrink-0 items-center justify-center rounded-xl bg-warning/15 text-warning">
        <AppIcon name="review" class="size-5" />
      </span>
      <span class="min-w-0 flex-1">
        <span class="block text-[15px] font-semibold">{{ pending.attendance + pending.leave }} waiting for your review</span>
        <span class="block text-[13px] text-hint">{{ pending.attendance }} attendance · {{ pending.leave }} leave</span>
      </span>
      <AppIcon name="chevron" class="size-5 shrink-0 text-hint" />
    </NuxtLink>

    <nav class="divide-y divide-separator overflow-hidden rounded-2xl bg-surface" aria-label="Shortcuts">
      <NuxtLink
        v-for="s in shortcuts"
        :key="s.to"
        :to="s.to"
        class="flex items-center gap-3 px-4 py-3 active:bg-page"
      >
        <span class="flex size-9 shrink-0 items-center justify-center rounded-xl" :class="s.tint">
          <AppIcon :name="s.icon" class="size-5" />
        </span>
        <span class="min-w-0 flex-1">
          <span class="block text-[15px] font-medium">{{ s.title }}</span>
          <span class="block truncate text-[13px] text-hint">{{ s.sub }}</span>
        </span>
        <AppIcon name="chevron" class="size-5 shrink-0 text-hint" />
      </NuxtLink>
    </nav>
  </div>
</template>
