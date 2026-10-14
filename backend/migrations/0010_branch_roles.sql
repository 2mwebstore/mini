-- 0010: roles can belong to one branch. The admins of a branch (Company Admin, Branch
-- Admin — users.branch) create and edit their branch's roles, with permissions they have
-- themselves; a branch role is given only to people of that branch. Company roles
-- (branch_id NULL) stay the owner's. Branch Admins now also manage the admin accounts
-- of their branch (Administration → System users, Branch assignment, Role management).
ALTER TABLE roles
  ADD COLUMN branch_id BIGINT UNSIGNED NULL AFTER description,
  ADD CONSTRAINT fk_roles_branch FOREIGN KEY (branch_id) REFERENCES branches(id);

INSERT IGNORE INTO role_permissions (role_id, permission) VALUES (5, 'users.branch');
