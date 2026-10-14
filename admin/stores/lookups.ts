import { defineStore } from 'pinia'
import { branchService } from '~/services/branch.service'
import { employeeService } from '~/services/employee.service'
import { leaveService } from '~/services/leave.service'
import { accessService } from '~/services/access.service'
import { scheduleService } from '~/services/schedule.service'
import { shiftHours } from '~/utils/format'
import type { Branch, Department, Employee, LeaveType, OrgItem, Role, WorkSchedule } from '~/types/api'
import type { SelectOption } from '~/utils/options'

// Reference data shared by filters and forms, fetched once per session.
export const useLookupsStore = defineStore('lookups', {
  state: () => ({
    employees: null as Employee[] | null,
    departments: null as Department[] | null,
    branches: null as Branch[] | null,
    schedules: null as WorkSchedule[] | null,
    leaveTypes: null as LeaveType[] | null,
    positions: null as OrgItem[] | null,
    roles: null as Role[] | null,
  }),
  getters: {
    // Ready-made <SearchSelect> options.
    branchOptions: (s): SelectOption[] => (s.branches ?? []).map(b => ({ value: String(b.id), label: b.name, sub: b.status === 'ACTIVE' ? undefined : 'Closed' })),
    departmentOptions: (s): SelectOption[] => (s.departments ?? []).map(d => ({ value: String(d.id), label: d.name })),
    employeeOptions: (s): SelectOption[] => (s.employees ?? []).map(e => ({ value: String(e.id), label: e.full_name, sub: e.employee_code })),
    positionOptions: (s): SelectOption[] => (s.positions ?? []).map(p => ({ value: String(p.id), label: p.name })),
    shiftOptions: (s): SelectOption[] =>
      (s.schedules ?? []).map(w => ({ value: String(w.id), label: w.name, sub: shiftHours(w.start_time, w.end_time, w.work_type === 'CROSS_DAY') })),
  },
  actions: {
    async loadEmployees(force = false) {
      if (!this.employees || force) this.employees = await employeeService.list({ include_inactive: true })
      return this.employees
    },
    async loadDepartments(force = false) {
      if (!this.departments || force) this.departments = await employeeService.departments()
      return this.departments
    },
    async loadBranches(force = false) {
      if (!this.branches || force) this.branches = await branchService.list()
      return this.branches
    },
    async loadSchedules(force = false) {
      if (!this.schedules || force) this.schedules = await scheduleService.shifts()
      return this.schedules
    },
    async loadPositions(force = false) {
      if (!this.positions || force) this.positions = await employeeService.orgList('positions')
      return this.positions
    },
    async loadRoles(force = false) {
      if (!this.roles || force) this.roles = await accessService.roles()
      return this.roles
    },
    async loadLeaveTypes() {
      if (!this.leaveTypes) this.leaveTypes = await leaveService.types()
      return this.leaveTypes
    },
    /** After creating/editing staff, so pickers show the change. */
    invalidateStaff() {
      this.employees = null
      this.departments = null
      this.branches = null // staff counts and admins change with staff
    },
  },
})
