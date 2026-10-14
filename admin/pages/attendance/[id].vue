<script setup lang="ts">
import { attendanceService } from '~/services/attendance.service'
import { useAuthStore } from '~/stores/auth'
import { formatDate } from '~/utils/format'

const auth = useAuthStore()
const route = useRoute()
const id = Number(route.params.id)

const detail = useResource(() => attendanceService.get(id))
const att = computed(() => detail.data.value?.attendance)
const editOpen = ref(false)
</script>

<template>
  <div>
    <NuxtLink to="/attendance" class="link text-sm">← Attendance</NuxtLink>
    <StateView :loading="detail.loading.value && !att" :error="detail.error.value" @retry="detail.load">
      <template v-if="detail.data.value && att">
        <PageHeader class="mt-3" :title="att.employee?.full_name ?? 'Attendance'" :subtitle="`${att.employee?.employee_code} · ${formatDate(att.work_date, true)}`">
          <AppButton v-if="auth.can('attendance.edit')" variant="secondary" @click="editOpen = true">Edit</AppButton>
        </PageHeader>
        <div class="card p-4 sm:p-5">
          <AttendanceDetailView :detail="detail.data.value" @changed="detail.load()" />
        </div>
      </template>
    </StateView>

    <AttendanceEditModal v-model="editOpen" :record="att ?? null" @saved="detail.load()" />
  </div>
</template>
