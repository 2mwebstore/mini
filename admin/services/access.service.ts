import type { Items, PermissionInfo, Role, RoleInput, SystemUser, SystemUserInput } from '~/types/api'
import { api } from './api'

// Roles & permissions, system users (panel logins) and their branch access.
export const accessService = {
  roles: () => api.get<Items<Role>>('/roles').then(r => r.items ?? []),
  permissions: () => api.get<Items<PermissionInfo>>('/roles/permissions').then(r => r.items ?? []),
  createRole: (input: RoleInput) => api.post<Role>('/roles', input),
  updateRole: (id: number, input: RoleInput) => api.put<Role>(`/roles/${id}`, input),
  deleteRole: (id: number) => api.del(`/roles/${id}`),

  users: () => api.get<Items<SystemUser>>('/system-users').then(r => r.items ?? []),
  createUser: (input: SystemUserInput) => api.post<SystemUser>('/system-users', input),
  updateUser: (id: number, input: SystemUserInput) => api.put<SystemUser>(`/system-users/${id}`, input),
  resetPassword: (id: number, password: string) => api.post(`/system-users/${id}/password`, { password }),
  deleteUser: (id: number) => api.del(`/system-users/${id}`),
  /** [] = every branch (for a company-wide role). */
  setBranches: (id: number, branchIds: number[]) => api.put<SystemUser>(`/system-users/${id}/branches`, { branch_ids: branchIds }),
}
