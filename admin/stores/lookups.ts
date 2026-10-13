import { defineStore } from 'pinia'
import { branchService } from '~/services/branch.service'
import { employeeService } from '~/services/employee.service'
import { leaveService } from '~/services/leave.service'
import type { Branch, Department, Employee, LeaveType, WorkSchedule } from '~/types/api'

// Reference data shared by filters and forms, fetched once per session.
export const useLookupsStore = defineStore('lookups', {
  state: () => ({
    employees: null as Employee[] | null,
    departments: null as Department[] | null,
    branches: null as Branch[] | null,
    schedules: null as WorkSchedule[] | null,
    leaveTypes: null as LeaveType[] | null,
  }),
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
    async loadSchedules() {
      if (!this.schedules) this.schedules = await employeeService.schedules()
      return this.schedules
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
