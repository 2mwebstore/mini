<script setup lang="ts">
import { branchService } from '~/services/branch.service'
import { useAuthStore } from '~/stores/auth'
import { useLookupsStore } from '~/stores/lookups'
import type { Branch } from '~/types/api'

// The owner sees every branch; a company admin sees the branches assigned to them
// or that they created (a new branch is theirs at once).
definePageMeta({ permission: ['branches.manage', 'branches.create'] })

const auth = useAuthStore()
const owner = computed(() => auth.can('branches.manage'))
const lookups = useLookupsStore()
const list = usePaged(p => branchService.page(p))
const { busy, run } = useAction()

const addOpen = ref(false)
const draft = reactive({ code: '', name: '', address: '', starter: true })
watch(addOpen, v => v && Object.assign(draft, { code: '', name: '', address: '', starter: true }))
function gpsText(b: Branch) {
  if (!b.gps_enabled) return 'Off'
  return ['On', b.gps_required && 'required', b.gps_reject_outside && 'in office'].filter(Boolean).join(' · ')
}
async function create() {
  let id = 0
  const ok = await run('create', async () => {
    id = (await branchService.create({ code: draft.code.trim(), name: draft.name.trim(), address: draft.address.trim(), starter: draft.starter })).id
  }, `${draft.name} added`)
  if (ok) {
    addOpen.value = false
    lookups.branches = null
    if (!owner.value) await auth.refresh() // the new branch is now within reach
    await navigateTo(`/branches/${id}`)
  }
}
</script>

<template>
  <div>
    <PageHeader
      title="Branches"
      :subtitle="owner ? 'Each branch has its own admin, Telegram bot, GPS rules and setup lists.' : 'Your branches. Each has its own Telegram bot, GPS rules and setup lists.'"
    >
      <AppButton @click="addOpen = true">Add branch</AppButton>
    </PageHeader>

    <StateView :loading="list.loading.value && !list.data.value" :error="list.error.value" :empty="list.total.value === 0" empty-text="No branches yet." @retry="list.load">
      <div class="card overflow-hidden" :class="{ 'opacity-60': list.loading.value }">
        <div class="overflow-x-auto">
        <table class="table">
          <thead><tr><th>Branch</th><th>Address</th><th>Branch admin</th><th>Telegram</th><th>GPS</th><th class="text-right">Staff</th><th>Status</th><th class="text-right">Actions</th></tr></thead>
          <tbody>
            <tr v-for="b in list.rows.value" :key="b.id" class="hover:bg-surface-2/60">
              <td>
                <NuxtLink :to="`/branches/${b.id}`" class="font-medium hover:underline">{{ b.name }}</NuxtLink>
                <span class="block font-mono text-xs text-muted">{{ b.code }}</span>
              </td>
              <td class="max-w-xs truncate text-muted">{{ b.address || '—' }}</td>
              <td>
                <template v-if="b.admin">
                  {{ b.admin.full_name }}
                  <span class="block text-xs text-muted">{{ b.admin.employee_code }}</span>
                </template>
                <StatusBadge v-else tone="warning" label="No admin" />
              </td>
              <td>
                <StatusBadge v-if="b.telegram_bot_set" tone="success" :label="b.telegram_bot_username ? `@${b.telegram_bot_username}` : 'Bot set'" />
                <StatusBadge v-else tone="warning" label="No bot" />
              </td>
              <td><StatusBadge :tone="b.gps_enabled ? 'info' : 'neutral'" :label="gpsText(b)" /></td>
              <td class="text-right tabular-nums">{{ b.staff_count }}</td>
              <td><StatusBadge :tone="b.status === 'ACTIVE' ? 'success' : 'neutral'" :label="b.status === 'ACTIVE' ? 'Open' : 'Closed'" /></td>
              <td class="whitespace-nowrap text-right">
                <div class="inline-flex gap-1.5">
                  <IconAction kind="view" :to="`/employees?branch_id=${b.id}`" :label="`View the staff of ${b.name}`" />
                  <IconAction kind="edit" :to="`/branches/${b.id}`" :label="`Edit ${b.name}: details, admin, GPS, Telegram`" />
                </div>
              </td>
            </tr>
          </tbody>
        </table>
        </div>
        <AppPagination :page="list.page.value" :page-size="list.pageSize.value" :total="list.total.value" noun="branch" @go="list.go" @size="list.resize" />
      </div>
    </StateView>

    <AppModal v-model="addOpen" title="Add branch">
      <form id="branch-form" class="grid gap-3 sm:grid-cols-3" @submit.prevent="create">
        <FormField label="Code" for="b-code" hint="e.g. SR">
          <input id="b-code" v-model="draft.code" class="input uppercase" required pattern="[A-Za-z0-9_\-]{2,32}" />
        </FormField>
        <FormField label="Name" for="b-name" class="sm:col-span-2">
          <input id="b-name" v-model="draft.name" class="input" required maxlength="150" placeholder="Siem Reap" />
        </FormField>
        <FormField label="Address" for="b-addr" class="sm:col-span-3">
          <input id="b-addr" v-model="draft.address" class="input" maxlength="255" />
        </FormField>
        <label class="flex items-start gap-2 text-sm sm:col-span-3">
          <input v-model="draft.starter" type="checkbox" class="mt-0.5 size-4 accent-[var(--primary)]" />
          <span>
            Start with a default shift and the usual leave types
            <span class="block text-xs text-muted">Office Hours 09:00–17:30 and Annual, Sick, Personal, Emergency and Unpaid leave — change them any time. Untick to set everything up yourself.</span>
          </span>
        </label>
      </form>
      <template #footer>
        <AppButton variant="secondary" @click="addOpen = false">Cancel</AppButton>
        <AppButton type="submit" form="branch-form" :loading="busy === 'create'">Add branch</AppButton>
      </template>
    </AppModal>
  </div>
</template>
