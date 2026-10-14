<script setup lang="ts">
import { auditService } from '~/services/audit.service'
import { useAuthStore } from '~/stores/auth'
import { formatDateTime } from '~/utils/format'

definePageMeta({ permission: 'audit.view' })

const auth = useAuthStore()
const actions = [
  'attendance.check_in', 'attendance.check_out', 'attendance.confirm', 'attendance.reject', 'attendance.manual_create', 'attendance.manual_edit',
  'attendance.auto_absent', 'leave.submit', 'leave.cancel', 'leave.approve', 'leave.reject', 'employee.create', 'employee.update',
  'employee.deactivate', 'user.create', 'settings.update', 'holiday.create', 'holiday.delete', 'report.export', 'auth.login', 'auth.logout',
]
const action = ref('')
const actionOptions = actions.map(a => ({ value: a, label: a }))
// Newest first, a page at a time.
const log = usePaged(p => auditService.page({ action: action.value || undefined }, p))
watch(action, () => log.reset())

const channelLabel: Record<string, string> = { MINIAPP: 'Mini App', DASHBOARD: 'Admin panel', SYSTEM: 'System', CLI: 'CLI', TELEGRAM: 'Telegram bot' }
</script>

<template>
  <div>
    <PageHeader title="Audit log" subtitle="Every change, append-only (the database refuses edits and deletes).">
      <SearchSelect v-model="action" class="w-56" label="Filter by action" :options="actionOptions" empty-label="All actions" />
    </PageHeader>

    <StateView :loading="log.loading.value && !log.data.value" :error="log.error.value" :empty="log.total.value === 0" empty-text="No entries." @retry="log.load">
      <div class="card overflow-hidden" :class="{ 'opacity-60': log.loading.value }">
        <div class="overflow-x-auto">
          <table class="table">
            <thead><tr><th>When</th><th>Who</th><th>Via</th><th>Action</th><th>Employee</th><th>Details</th></tr></thead>
            <tbody>
              <tr v-for="l in log.rows.value" :key="l.id" class="align-top">
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
        <AppPagination :page="log.page.value" :page-size="log.pageSize.value" :total="log.total.value" noun="entry" @go="log.go" @size="log.resize" />
      </div>
    </StateView>
  </div>
</template>
