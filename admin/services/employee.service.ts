import type { Department, Employee, EmployeeInput, OrgItem } from '~/types/api'
import { api, listAll, listPage, type Paging } from './api'

type EmployeeQuery = { q?: string; department_id?: number; position_id?: number; branch_id?: number; status?: string; include_inactive?: boolean }

export const employeeService = {
  /** Everyone (pickers); `page` for the Employees list. */
  list: (query: EmployeeQuery = {}) => listAll<Employee>('/employees', query),
  page: (query: EmployeeQuery, p: Paging) => listPage<Employee>('/employees', query, p),
  get: (id: number) => api.get<Employee>(`/employees/${id}`),
  create: (input: EmployeeInput) => api.post<Employee>('/employees', input),
  update: (id: number, input: EmployeeInput) => api.put<Employee>(`/employees/${id}`, input),
  deactivate: (id: number) => api.del(`/employees/${id}`),
  departments: (branchId?: number) => listAll<Department>('/departments', { branch_id: branchId || undefined }),
  // Managed lists, each item belongs to one branch: departments and positions.
  orgList: (kind: 'departments' | 'positions', branchId?: number) => listAll<OrgItem>(`/${kind}`, { branch_id: branchId || undefined }),
  orgPage: (kind: 'departments' | 'positions', branchId: number | undefined, p: Paging) =>
    listPage<OrgItem>(`/${kind}`, { branch_id: branchId || undefined }, p),
  orgSave: (kind: 'departments' | 'positions', id: number, name: string, branchId?: number) =>
    id ? api.put<OrgItem>(`/${kind}/${id}`, { name }) : api.post<OrgItem>(`/${kind}`, { name, branch_id: branchId }),
  orgDelete: (kind: 'departments' | 'positions', id: number) => api.del(`/${kind}/${id}`),
}
