<script setup lang="ts">
import { employeeService } from '~/services/employee.service'
import { useLookupsStore } from '~/stores/lookups'
import type { EmployeeInput } from '~/types/api'

definePageMeta({ permission: 'staff.manage' })

const lookups = useLookupsStore()
const { busy, run } = useAction()

async function create(input: EmployeeInput) {
  let id = 0
  const ok = await run('create', async () => {
    id = (await employeeService.create(input)).id
  }, `${input.full_name} added`)
  if (ok) {
    lookups.invalidateStaff()
    await navigateTo(`/employees/${id}`)
  }
}
</script>

<template>
  <div>
    <NuxtLink to="/employees" class="link text-sm">← Employees</NuxtLink>
    <PageHeader class="mt-3" title="Add employee" subtitle="Add their Telegram user ID so they can use the Mini App." />
    <EmployeeForm :saving="busy === 'create'" @submit="create" />
  </div>
</template>
