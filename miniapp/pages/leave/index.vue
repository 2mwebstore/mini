<script setup lang="ts">
import type { ApiError } from '~/services/api'
import { leaveService } from '~/services/leave.service'
import type { LeaveRequest } from '~/types/api'

const { data: items, error, loading, load } = useResource(() => leaveService.mine())
const cancelling = ref<number | null>(null)

async function cancel(l: LeaveRequest) {
  if (!(await confirmDialog('Cancel this leave request?'))) return
  cancelling.value = l.id
  try {
    await leaveService.cancel(l.id)
    haptic('success')
    await load()
  } catch (e) {
    haptic('error')
    await alertDialog((e as ApiError).message)
  } finally {
    cancelling.value = null
  }
}
</script>

<template>
  <div class="flex flex-col gap-4">
    <header class="flex items-center justify-between">
      <h1 class="text-2xl font-bold">Leave</h1>
      <AppButton variant="secondary" class="py-2" @click="navigateTo('/leave/new')">
        <AppIcon name="plus" class="size-4" />New request
      </AppButton>
    </header>

    <StateView :loading="loading && !items" :error="error" :empty="!items?.length" empty-text="You have no leave requests yet." @retry="load">
      <template #empty-action>
        <AppButton @click="navigateTo('/leave/new')">Request leave</AppButton>
      </template>
      <AppCard>
        <div class="-my-3 divide-y divide-separator">
          <LeaveItem v-for="l in items" :key="l.id" :leave="l">
            <AppButton
              v-if="l.status === 'PENDING'"
              variant="ghost"
              class="-ml-4 mt-1 py-1 text-danger"
              :loading="cancelling === l.id"
              @click="cancel(l)"
            >Cancel request</AppButton>
          </LeaveItem>
        </div>
      </AppCard>
    </StateView>
  </div>
</template>
