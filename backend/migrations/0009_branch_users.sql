-- 0009: a client's Company Admin manages the admin accounts of their own branches
-- (System users and Branch assignment, limited to those branches; roles with no more
-- powers than their own). The owner can grant the same to other roles on Role management.
INSERT IGNORE INTO role_permissions (role_id, permission)
SELECT id, 'users.branch' FROM roles WHERE code = 'company_admin';
