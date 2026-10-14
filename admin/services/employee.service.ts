import type { Department, Employee, EmployeeInput, Items, OrgItem } from '~/types/api'
import { api } from './api'

export const employeeService = {
  list: (query: { q?: string; department_id?: number; position_id?: number; branch_id?: number; status?: string; include_inactive?: boolean } = {}) =>
    api.get<Items<Employee>>('/employees', query).then(r => r.items ?? []),
  get: (id: number) => api.get<Employee>(`/employees/${id}`),
  create: (input: EmployeeInput) => api.post<Employee>('/employees', input),
  update: (id: number, input: EmployeeInput) => api.put<Employee>(`/employees/${id}`, input),
  deactivate: (id: number) => api.del(`/employees/${id}`),
  departments: () => api.get<Items<Department>>('/departments').then(r => r.items ?? []),
  // Managed lists (company-wide): departments and positions.
  orgList: (kind: 'departments' | 'positions') => api.get<Items<OrgItem>>(`/${kind}`).then(r => r.items ?? []),
  orgSave: (kind: 'departments' | 'positions', id: number, name: string) =>
    id ? api.put<OrgItem>(`/${kind}/${id}`, { name }) : api.post<OrgItem>(`/${kind}`, { name }),
  orgDelete: (kind: 'departments' | 'positions', id: number) => api.del(`/${kind}/${id}`),
}
