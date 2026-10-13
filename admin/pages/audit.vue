<script setup lang="ts">
import type { ApiError } from '~/services/api'
import { auditService } from '~/services/audit.service'
import { useAuthStore } from '~/stores/auth'
import type { AuditLog } from '~/types/api'
import { formatDateTime } from '~/utils/format'

definePageMeta({ permission: 'audit.view' })

const auth = useAuthStore()
const actions = [
  'attendance.check_in', 'attendance.check_out', 'attendance.confirm', 'attendance.reject', 'attendance.manual_create', 'attendance.manual_edit',
  'attendance.auto_absent', 'leave.submit', 'leave.cancel', 'leave.approve', 'leave.reject', 'employee.create', 'employee.update',
  'employee.deactivate', 'user.create', 'settings.update', 'holiday.create', 'holiday.delete', 'report.export', 'auth.login', 'auth.logout',
]
const action = ref('')
const items = ref<AuditLog[]>([])
const loading = ref(false)
const error = ref<ApiError | null>(null)
const done = ref(false)
const PAGE = 100

async function load(more = false) {
  loading.value = true
  error.value = null
  try {
    const before = more ? items.value[items.value.length - 1]?.id : undefined
    const page = await auditService.list({ action: action.value || undefined, before_id: before, limit: PAGE })
    items.value = more ? [...items.value, ...page] : page
    done.value = page.length < PAGE
  } catch (e) {
    error.value = e as ApiError
  } finally {
    loading.value = false
  }
}
onMounted(() => load())
watch(action, () => load())

const channelLabel: Record<string, string> = { MINIAPP: 'Mini App', DASHBOARD: 'Admin panel', SYSTEM: 'System', CLI: 'CLI', TELEGRAM: 'Telegram bot' }
</script>

<template>
  <div>
    <PageHeader title="Audit log" subtitle="Every change, append-only (the database refuses edits and deletes).">
      <select v-model="action" class="input w-56" aria-label="Filter by action">
        <option value="">All actions</option>
        <option v-for="a in actions" :key="a" :value="a">{{ a }}</option>
      </select>
    </PageHeader>

    <StateView :loading="loading && !items.length" :error="items.length ? null : error" :empty="!items.length" empty-text="No entries." @retry="load()">
      <div class="card overflow-x-auto">
        <table class="table">
          <thead><tr><th>When</th><th>Who</th><th>Via</th><th>Action</th><th>Employee</th><th>Details</th></tr></thead>
          <tbody>
            <tr v-for="l in items" :key="l.id" class="align-top">
              <td class="whitespace-nowrap tabular-nums text-muted">{{ formatDateTime(l.created_at, auth.company.timezone) }}</td>
              <td>{{ l.actor?.full_name ?? 'System' }}<span v-if="l.ip" class="block font-mono text-xs text-muted">{{ l.ip }}</span></td>
              <td class="text-muted">{{ channelLabel[l.channel] ?? l.channel }}</td>
              <td class="font-mono text-xs">{{ l.action }}</td>
              <td>{{ l.target?.full_name ?? '—' }}</td>
              <td class="max-w-sm">
                <details v-if="l.details">
                  <summary class="cursor-pointer text-xs text-primary">Show</summary>
                  <pre class="mt-1 max-h-60 overflow-auto rounded-md bg-surface-2 p-2 text-xs">{{ JSON.stringify(l.details, null, 2) }}</pre>
                </details>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <div class="mt-4 flex justify-center">
        <AppButton v-if="!done" variant="secondary" :loading="loading" @click="load(true)">Load older</AppButton>
      </div>
    </StateView>
  </div>
</template>
