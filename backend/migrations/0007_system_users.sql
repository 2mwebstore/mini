-- 0007: system users are separate from employees.
-- A person is either an EMPLOYEE (staff who check in and out in the Mini App, listed
-- on Employees) or a SYSTEM user (an admin panel account, listed on Administration →
-- System users). Only system users sign in to the panel; employees never do.
ALTER TABLE employees
  ADD COLUMN kind VARCHAR(16) NOT NULL DEFAULT 'STAFF' AFTER id, -- STAFF | SYSTEM
  ADD KEY idx_employees_kind (kind);

-- Every existing panel login, sorted into what it is.
CREATE TEMPORARY TABLE mig_logins (
  employee_id BIGINT UNSIGNED NOT NULL PRIMARY KEY,
  admin_role  TINYINT(1) NOT NULL, -- the role has powers beyond a manager's
  checked_in  TINYINT(1) NOT NULL, -- they have also worked as staff
  telegram    TINYINT(1) NOT NULL
);
INSERT INTO mig_logins
SELECT e.id,
       e.role_id IN (1, 5) OR EXISTS (SELECT 1 FROM role_permissions rp WHERE rp.role_id = e.role_id
         AND rp.permission NOT IN ('attendance.self', 'leave.self', 'team.view', 'team.review', 'report.export')),
       EXISTS (SELECT 1 FROM attendance a WHERE a.employee_id = e.id AND a.check_in_at IS NOT NULL),
       EXISTS (SELECT 1 FROM telegram_accounts t WHERE t.employee_id = e.id)
FROM employees e JOIN users u ON u.employee_id = e.id;

-- (A temporary table can be read only once per statement, hence the variables.)
SET @mig_became = (SELECT JSON_ARRAYAGG(employee_id) FROM mig_logins WHERE (admin_role = 1 AND checked_in = 0) OR (admin_role = 0 AND checked_in = 0 AND telegram = 0));
SET @mig_split = (SELECT JSON_ARRAYAGG(employee_id) FROM mig_logins WHERE admin_role = 1 AND checked_in = 1);
SET @mig_removed = (SELECT JSON_ARRAYAGG(employee_id) FROM mig_logins WHERE admin_role = 0 AND (checked_in = 1 OR telegram = 1));
INSERT INTO audit_logs (channel, action, entity_type, details)
VALUES ('SYSTEM', 'migration.system_users', 'user', JSON_OBJECT(
  'became_system_users', CAST(COALESCE(@mig_became, '[]') AS JSON),
  'split_into_employee_and_system_user', CAST(COALESCE(@mig_split, '[]') AS JSON),
  'panel_login_removed', CAST(COALESCE(@mig_removed, '[]') AS JSON)));

-- 1. Admins who never checked in are system users as they are (owner, office HR…).
UPDATE employees e JOIN mig_logins m ON m.employee_id = e.id SET e.kind = 'SYSTEM'
WHERE m.admin_role = 1 AND m.checked_in = 0;

-- 2. Admins who also checked in become two records: a new system user takes the
--    login, role, branch access and branch-admin assignment; their employee record
--    keeps the attendance history and Telegram link, as Manager if people report to
--    them (they keep reviewing them in the Mini App), otherwise as Staff.
INSERT INTO employees (kind, employee_code, full_name, branch_id, phone, email, role_id, status, created_at, deleted_at)
SELECT 'SYSTEM', CONCAT('SU-', e.id), e.full_name, e.branch_id, e.phone, e.email, e.role_id, e.status, e.created_at, e.deleted_at
FROM employees e JOIN mig_logins m ON m.employee_id = e.id
WHERE m.admin_role = 1 AND m.checked_in = 1;

CREATE TEMPORARY TABLE mig_split (staff_id BIGINT UNSIGNED NOT NULL PRIMARY KEY, system_id BIGINT UNSIGNED NOT NULL);
INSERT INTO mig_split
SELECT e.id, s.id FROM employees e
JOIN mig_logins m ON m.employee_id = e.id AND m.admin_role = 1 AND m.checked_in = 1
JOIN employees s ON s.employee_code = CONCAT('SU-', e.id) AND s.kind = 'SYSTEM';

CREATE TEMPORARY TABLE mig_has_team (id BIGINT UNSIGNED NOT NULL PRIMARY KEY);
INSERT INTO mig_has_team SELECT DISTINCT manager_id FROM employees WHERE manager_id IS NOT NULL AND deleted_at IS NULL;

UPDATE users u JOIN mig_split x ON x.staff_id = u.employee_id SET u.employee_id = x.system_id;
UPDATE user_branches ub JOIN mig_split x ON x.staff_id = ub.employee_id SET ub.employee_id = x.system_id;
UPDATE branches b JOIN mig_split x ON x.staff_id = b.admin_employee_id SET b.admin_employee_id = x.system_id;
UPDATE employees e JOIN mig_split x ON x.staff_id = e.id LEFT JOIN mig_has_team t ON t.id = e.id
SET e.role_id = IF(t.id IS NULL, 4, 3);

-- 3. Managers and staff use the Mini App: a login of someone who works as staff is
--    removed (they keep reviewing their team in the Mini App). A staff-level login
--    of someone who never checked in and has no Telegram becomes a system user.
UPDATE employees e JOIN mig_logins m ON m.employee_id = e.id SET e.kind = 'SYSTEM'
WHERE m.admin_role = 0 AND m.checked_in = 0 AND m.telegram = 0;
DELETE ub FROM user_branches ub JOIN mig_logins m ON m.employee_id = ub.employee_id
WHERE m.admin_role = 0 AND (m.checked_in = 1 OR m.telegram = 1);
DELETE u FROM users u JOIN mig_logins m ON m.employee_id = u.employee_id
WHERE m.admin_role = 0 AND (m.checked_in = 1 OR m.telegram = 1);

DROP TEMPORARY TABLE mig_logins;
DROP TEMPORARY TABLE mig_split;
DROP TEMPORARY TABLE mig_has_team;
