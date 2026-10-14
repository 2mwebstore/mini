<script setup lang="ts">
import type { ApiError } from '~/services/api'
import { leaveService } from '~/services/leave.service'
import type { LeaveRequest } from '~/types/api'

const { data: items, error, loading, load } = useResource(() => leaveService.mine())
const balances = useResource(async () => (await leaveService.balances()).filter(b => b.tracked))
const fmt = (n: number) => (n % 1 ? n.toFixed(1) : String(n))
const cancelling = ref<number | null>(null)

async function cancel(l: LeaveRequest) {
  if (!(await confirmDialog('Cancel this leave request?'))) return
  cancelling.value = l.id
  try {
    await leaveService.cancel(l.id)
    haptic('success')
    await load()
    balances.load()
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

    <AppCard v-if="balances.data.value?.length" :title="`Balance ${balances.data.value[0]!.year}`">
      <ul class="flex flex-col gap-3">
        <li v-for="b in balances.data.value" :key="b.leave_type_id">
          <div class="flex items-baseline justify-between gap-2 text-[15px]">
            <span class="font-medium">{{ b.name }}</span>
            <span class="tabular-nums"><b>{{ fmt(Math.max(0, b.remaining)) }}</b> <span class="text-hint">of {{ fmt(b.allocated + b.carried) }} days left</span></span>
          </div>
          <div class="mt-1.5 h-1.5 overflow-hidden rounded-full bg-page" aria-hidden="true">
            <div class="h-full rounded-full" :style="{ width: `${Math.min(100, (b.allocated + b.carried) > 0 ? (b.remaining / (b.allocated + b.carried)) * 100 : 0)}%`, background: b.color }" />
          </div>
          <p v-if="b.pending" class="mt-1 text-[13px] text-warning">{{ fmt(b.pending) }} day{{ b.pending === 1 ? '' : 's' }} waiting for approval</p>
        </li>
      </ul>
    </AppCard>

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
