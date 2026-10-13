<script setup lang="ts">
import { useAuthStore } from '~/stores/auth'

const auth = useAuthStore()
const telegramId = webApp()?.initDataUnsafe.user?.id
const dayNames = ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun']

const rows = computed(() => {
  const p = auth.profile
  if (!p) return []
  return [
    { label: 'Employee ID', value: p.employee_code },
    { label: 'Branch', value: p.branch || 'All branches' },
    { label: 'Department', value: p.department || '—' },
    { label: 'Position', value: p.position || '—' },
    { label: 'Role', value: p.role_name || auth.me?.role },
    { label: 'Schedule', value: `${p.schedule.start} – ${p.schedule.end}` },
    { label: 'Work days', value: p.schedule.work_days.map(d => dayNames[d - 1]).join(', ') },
    { label: 'Grace period', value: `${p.schedule.grace_minutes} min` },
    { label: 'Telegram ID', value: telegramId ? String(telegramId) : '—' },
  ]
})
</script>

<template>
  <div class="flex flex-col gap-4">
    <header class="flex flex-col items-center gap-2 pt-2 text-center">
      <div class="flex size-20 items-center justify-center rounded-full bg-primary text-3xl font-semibold text-primary-fg" aria-hidden="true">
        {{ auth.profile?.full_name.charAt(0) }}
      </div>
      <h1 class="text-xl font-bold">{{ auth.profile?.full_name }}</h1>
      <p class="text-[15px] text-hint">{{ auth.settings?.company_name }}</p>
    </header>

    <AppCard>
      <dl class="-my-3 divide-y divide-separator">
        <div v-for="r in rows" :key="r.label" class="flex justify-between gap-4 py-3 text-[15px]">
          <dt class="text-hint">{{ r.label }}</dt>
          <dd class="text-right font-medium">{{ r.value }}</dd>
        </div>
      </dl>
    </AppCard>
  </div>
</template>
