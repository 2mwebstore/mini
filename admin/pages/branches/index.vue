<script setup lang="ts">
import { branchService } from '~/services/branch.service'
import { useLookupsStore } from '~/stores/lookups'

definePageMeta({ permission: 'branches.manage' })

const lookups = useLookupsStore()
const list = useResource(() => branchService.list())
const { busy, run } = useAction()

const addOpen = ref(false)
const draft = reactive({ code: '', name: '', address: '' })
watch(addOpen, v => v && Object.assign(draft, { code: '', name: '', address: '' }))
async function create() {
  let id = 0
  const ok = await run('create', async () => {
    id = (await branchService.create({ code: draft.code.trim(), name: draft.name.trim(), address: draft.address.trim() })).id
  }, `${draft.name} added`)
  if (ok) {
    addOpen.value = false
    lookups.branches = null
    await navigateTo(`/branches/${id}`)
  }
}
</script>

<template>
  <div>
    <PageHeader title="Branches" subtitle="Each branch has one admin who manages only that branch.">
      <AppButton @click="addOpen = true">Add branch</AppButton>
    </PageHeader>

    <StateView :loading="list.loading.value && !list.data.value" :error="list.error.value" :empty="list.data.value?.length === 0" empty-text="No branches yet." @retry="list.load">
      <div class="card overflow-x-auto">
        <table class="table">
          <thead><tr><th>Branch</th><th>Address</th><th>Branch admin</th><th class="text-right">Staff</th><th>Status</th><th /></tr></thead>
          <tbody>
            <tr v-for="b in list.data.value ?? []" :key="b.id" class="hover:bg-surface-2/60">
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
              <td class="text-right tabular-nums">{{ b.staff_count }}</td>
              <td><StatusBadge :tone="b.status === 'ACTIVE' ? 'success' : 'neutral'" :label="b.status === 'ACTIVE' ? 'Open' : 'Closed'" /></td>
              <td class="text-right"><NuxtLink :to="`/branches/${b.id}`" class="link text-sm">Manage</NuxtLink></td>
            </tr>
          </tbody>
        </table>
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
      </form>
      <template #footer>
        <AppButton variant="secondary" @click="addOpen = false">Cancel</AppButton>
        <AppButton type="submit" form="branch-form" :loading="busy === 'create'">Add branch</AppButton>
      </template>
    </AppModal>
  </div>
</template>
