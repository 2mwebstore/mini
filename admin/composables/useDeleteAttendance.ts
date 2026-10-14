import { attendanceService } from '~/services/attendance.service'
import { useAuthStore } from '~/stores/auth'
import type { Attendance } from '~/types/api'
import { formatClock, formatDate } from '~/utils/format'

// Deleting an attendance day: asks for a reason (audited) in the confirmation
// dialog, then deletes. Resolves true when the record is gone.
export function useDeleteAttendance() {
  const auth = useAuthStore()
  const { confirm } = useConfirm()
  const { busy, run } = useAction()
  async function remove(a: Attendance): Promise<boolean> {
    const tz = auth.company.timezone
    const times = !a.check_in_at ? 'No check-in'
      : a.check_out_at ? `${formatClock(a.check_in_at, tz)} – ${formatClock(a.check_out_at, tz)}` : `In ${formatClock(a.check_in_at, tz)}, no check-out yet`
    const answer = await confirm({
      title: 'Delete this attendance record?',
      message: 'The day, its reviews, locations and photos are removed. The record and your reason are kept in the audit log.',
      details: [{ label: 'Employee', value: a.employee?.full_name ?? `#${a.employee_id}` }, { label: 'Date', value: formatDate(a.work_date, true) },
        { label: 'Times', value: times }],
      reason: { required: true, presets: ['Duplicate record', 'Recorded by mistake', 'Test record'], placeholder: 'Why is it being deleted?' },
      confirmLabel: 'Delete record',
    })
    if (!answer) return false
    return run(`del${a.id}`, () => attendanceService.remove(a.id, answer.reason), 'Attendance record deleted')
  }
  return { remove, busy }
}
