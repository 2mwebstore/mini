import { defineStore } from 'pinia'
import { branchService } from '~/services/branch.service'
import { employeeService } from '~/services/employee.service'
import { leaveService } from '~/services/leave.service'
import { accessService } from '~/services/access.service'
import { scheduleService } from '~/services/schedule.service'
import { shiftHours } from '~/utils/format'
import type { Branch, Department, Employee, LeaveType, OrgItem, Role, WorkSchedule } from '~/types/api'
import type { SelectOption } from '~/utils/options'

/** A branch id as stored in forms ("" / 0 / undefined = no branch chosen). */
type BranchRef = string | number | null | undefined

function ofBranch<T extends { branch_id: number }>(items: T[] | null, branchId?: BranchRef): T[] {
  const id = Number(branchId) || 0
  return (items ?? []).filter(i => !id || i.branch_id === id)
}

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
    employeeOptions: (s): SelectOption[] => (s.employees ?? []).map(e => ({ value: String(e.id), label: e.full_name, sub: e.employee_code })),
    /**
     * Departments, positions and shifts each belong to one branch. With a branch:
     * that branch's only; without: all of them, labelled with their branch when
     * there are several (two branches may both have "Sales").
     */
    departmentOptions(): (branchId?: BranchRef) => SelectOption[] {
      return branchId => ofBranch(this.departments, branchId).map(d => ({ value: String(d.id), label: d.name, sub: this.branchLabel(d.branch_id, branchId) }))
    },
    positionOptions(): (branchId?: BranchRef) => SelectOption[] {
      return branchId => ofBranch(this.positions, branchId).map(p => ({ value: String(p.id), label: p.name, sub: this.branchLabel(p.branch_id, branchId) }))
    },
    shiftOptions(): (branchId?: BranchRef) => SelectOption[] {
      return branchId => ofBranch(this.schedules, branchId).map(w => {
        const hours = shiftHours(w.start_time, w.end_time, w.work_type === 'CROSS_DAY')
        const where = this.branchLabel(w.branch_id, branchId)
        return { value: String(w.id), label: w.name, sub: where ? `${hours} · ${where}` : hours }
      })
    },
    /** Leave types are per branch too (the active ones the actor's branches use). */
    leaveTypeOptions(): (branchId?: BranchRef) => SelectOption[] {
      return branchId => ofBranch(this.leaveTypes, branchId).map(t => ({ value: String(t.id), label: t.name, sub: this.branchLabel(t.branch_id, branchId) }))
    },
    /** The branch's name as a sub-label, only when items of several branches are listed together. */
    branchLabel(s) {
      const names = new Map((s.branches ?? []).map(b => [b.id, b.name]))
      return (itemBranch: number, chosen?: BranchRef): string | undefined =>
        Number(chosen) || names.size < 2 ? undefined : names.get(itemBranch)
    },
  },
  actions: {
    /** Whether a chosen item (by id, "" = none) belongs to the branch ("" = any). */
    fits(kind: 'departments' | 'positions' | 'schedules' | 'leaveTypes', id: string | number | null | undefined, branchId: BranchRef): boolean {
      const b = Number(branchId) || 0
      if (!b || !Number(id)) return true
      return (this[kind] ?? []).some(i => i.id === Number(id) && i.branch_id === b)
    },
    async loadEmployees(force = false) {
      if (!this.employees || force) this.employees = await employeeService.list({ include_inactive: true })
      return this.employees
    },
    // Lists of per-branch items also load the branches, for their labels.
    async loadDepartments(force = false) {
      if (!this.departments || force) [this.departments] = await Promise.all([employeeService.departments(), this.loadBranches()])
      return this.departments
    },
    async loadBranches(force = false) {
      if (!this.branches || force) this.branches = await branchService.list()
      return this.branches
    },
    async loadSchedules(force = false) {
      if (!this.schedules || force) [this.schedules] = await Promise.all([scheduleService.shifts(), this.loadBranches()])
      return this.schedules
    },
    async loadPositions(force = false) {
      if (!this.positions || force) [this.positions] = await Promise.all([employeeService.orgList('positions'), this.loadBranches()])
      return this.positions
    },
    async loadRoles(force = false) {
      if (!this.roles || force) this.roles = await accessService.roles()
      return this.roles
    },
    async loadLeaveTypes(force = false) {
      if (!this.leaveTypes || force) [this.leaveTypes] = await Promise.all([leaveService.types(), this.loadBranches()])
      return this.leaveTypes
    },
    /** After a branch's setup lists change (departments, positions, shifts, leave types). */
    invalidateSetup() {
      this.departments = null
      this.positions = null
      this.schedules = null
      this.leaveTypes = null
    },
    /** After creating/editing staff, so pickers show the change. */
    invalidateStaff() {
      this.employees = null
      this.departments = null
      this.branches = null // staff counts and admins change with staff
      this.departments = null // new names typed in the staff form are created
      this.positions = null
    },
  },
})
