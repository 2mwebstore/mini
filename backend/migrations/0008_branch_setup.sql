-- 0008: everything belongs to a branch.
-- A company is set up branch by branch: each branch has its own departments,
-- positions, shifts, leave types, holidays, Telegram bot and GPS rules — nothing is
-- shared between branches. Existing shared items are copied into every branch (each
-- person's records follow the copy in their branch), then the shared originals go.
-- Admins can also create branches (new permission branches.create), and a "Company
-- Admin" role runs the branches it is given or creates. Check-ins and check-outs get
-- the employee's reason (required when late / leaving early).

SET @first_branch = (SELECT MIN(id) FROM branches);
-- Staff always work in a branch; system users have no department, position or shift.
UPDATE employees SET branch_id = @first_branch WHERE kind = 'STAFF' AND branch_id IS NULL AND @first_branch IS NOT NULL;
UPDATE employees SET department_id = NULL, position_id = NULL, work_schedule_id = NULL, manager_id = NULL WHERE kind = 'SYSTEM';

-- ---------- departments ----------
ALTER TABLE departments DROP INDEX name, ADD COLUMN branch_id BIGINT UNSIGNED NULL AFTER id, ADD COLUMN src_id BIGINT UNSIGNED NULL;
INSERT INTO departments (branch_id, name, src_id, created_at, updated_at)
SELECT b.id, d.name, d.id, d.created_at, d.updated_at FROM departments d CROSS JOIN branches b WHERE d.branch_id IS NULL;
UPDATE employees e JOIN departments d ON d.src_id = e.department_id AND d.branch_id = COALESCE(e.branch_id, @first_branch) SET e.department_id = d.id;
DELETE FROM departments WHERE branch_id IS NULL;
ALTER TABLE departments DROP COLUMN src_id, MODIFY branch_id BIGINT UNSIGNED NOT NULL,
  ADD UNIQUE KEY uq_departments_branch_name (branch_id, name),
  ADD CONSTRAINT fk_departments_branch FOREIGN KEY (branch_id) REFERENCES branches(id);

-- ---------- positions ----------
ALTER TABLE positions DROP INDEX name, ADD COLUMN branch_id BIGINT UNSIGNED NULL AFTER id, ADD COLUMN src_id BIGINT UNSIGNED NULL;
INSERT INTO positions (branch_id, name, src_id, created_at, updated_at)
SELECT b.id, p.name, p.id, p.created_at, p.updated_at FROM positions p CROSS JOIN branches b WHERE p.branch_id IS NULL;
UPDATE employees e JOIN positions p ON p.src_id = e.position_id AND p.branch_id = COALESCE(e.branch_id, @first_branch) SET e.position_id = p.id;
DELETE FROM positions WHERE branch_id IS NULL;
ALTER TABLE positions DROP COLUMN src_id, MODIFY branch_id BIGINT UNSIGNED NOT NULL,
  ADD UNIQUE KEY uq_positions_branch_name (branch_id, name),
  ADD CONSTRAINT fk_positions_branch FOREIGN KEY (branch_id) REFERENCES branches(id);

-- ---------- shifts (each branch has its own default) ----------
ALTER TABLE work_schedules DROP INDEX name, ADD COLUMN branch_id BIGINT UNSIGNED NULL AFTER id, ADD COLUMN src_id BIGINT UNSIGNED NULL;
INSERT INTO work_schedules (branch_id, name, start_time, end_time, work_type, grace_minutes, work_days, color, is_default, src_id, created_at, updated_at)
SELECT b.id, w.name, w.start_time, w.end_time, w.work_type, w.grace_minutes, w.work_days, w.color, w.is_default, w.id, w.created_at, w.updated_at
FROM work_schedules w CROSS JOIN branches b WHERE w.branch_id IS NULL;
UPDATE employees e JOIN work_schedules w ON w.src_id = e.work_schedule_id AND w.branch_id = COALESCE(e.branch_id, @first_branch) SET e.work_schedule_id = w.id;
UPDATE roster_shifts r JOIN employees e ON e.id = r.employee_id
  JOIN work_schedules w ON w.src_id = r.work_schedule_id AND w.branch_id = COALESCE(e.branch_id, @first_branch) SET r.work_schedule_id = w.id;
UPDATE schedule_overrides o JOIN employees e ON e.id = o.employee_id
  JOIN work_schedules w ON w.src_id = o.work_schedule_id AND w.branch_id = COALESCE(e.branch_id, @first_branch) SET o.work_schedule_id = w.id;
DELETE FROM work_schedules WHERE branch_id IS NULL;
ALTER TABLE work_schedules DROP COLUMN src_id, MODIFY branch_id BIGINT UNSIGNED NOT NULL,
  ADD UNIQUE KEY uq_work_schedules_branch_name (branch_id, name),
  ADD CONSTRAINT fk_work_schedules_branch FOREIGN KEY (branch_id) REFERENCES branches(id);

-- ---------- leave types (requests and balances follow the copy in the person's branch) ----------
ALTER TABLE leave_types DROP INDEX code, ADD COLUMN branch_id BIGINT UNSIGNED NULL AFTER id, ADD COLUMN src_id BIGINT UNSIGNED NULL;
INSERT INTO leave_types (branch_id, code, name, annual_days, is_paid, approval, allow_exceed, max_days_per_request, min_notice_days,
  color, description, active, sort_order, src_id, created_at, updated_at)
SELECT b.id, t.code, t.name, t.annual_days, t.is_paid, t.approval, t.allow_exceed, t.max_days_per_request, t.min_notice_days,
  t.color, t.description, t.active, t.sort_order, t.id, t.created_at, t.updated_at
FROM leave_types t CROSS JOIN branches b WHERE t.branch_id IS NULL;
UPDATE leave_requests r JOIN employees e ON e.id = r.employee_id
  JOIN leave_types t ON t.src_id = r.leave_type_id AND t.branch_id = COALESCE(e.branch_id, @first_branch) SET r.leave_type_id = t.id;
UPDATE leave_balances l JOIN employees e ON e.id = l.employee_id
  JOIN leave_types t ON t.src_id = l.leave_type_id AND t.branch_id = COALESCE(e.branch_id, @first_branch) SET l.leave_type_id = t.id;
DELETE FROM leave_types WHERE branch_id IS NULL;
ALTER TABLE leave_types DROP COLUMN src_id, MODIFY branch_id BIGINT UNSIGNED NOT NULL,
  ADD UNIQUE KEY uq_leave_types_branch_code (branch_id, code),
  ADD CONSTRAINT fk_leave_types_branch FOREIGN KEY (branch_id) REFERENCES branches(id);

-- ---------- holidays ----------
ALTER TABLE holidays DROP INDEX date, ADD COLUMN branch_id BIGINT UNSIGNED NULL AFTER id;
INSERT INTO holidays (branch_id, date, name, created_at)
SELECT b.id, h.date, h.name, h.created_at FROM holidays h CROSS JOIN branches b WHERE h.branch_id IS NULL;
DELETE FROM holidays WHERE branch_id IS NULL;
ALTER TABLE holidays MODIFY branch_id BIGINT UNSIGNED NOT NULL,
  ADD UNIQUE KEY uq_holidays_branch_date (branch_id, date),
  ADD CONSTRAINT fk_holidays_branch FOREIGN KEY (branch_id) REFERENCES branches(id);

-- ---------- Telegram: each branch has its own bot ----------
-- The bot token stays encrypted (same key); the company bot and chats become each branch's.
ALTER TABLE branches
  ADD COLUMN telegram_bot_token    VARCHAR(512) NOT NULL DEFAULT '' AFTER address, -- encrypted; never sent to clients
  ADD COLUMN telegram_bot_username VARCHAR(64)  NOT NULL DEFAULT '' AFTER telegram_bot_token,
  ADD COLUMN miniapp_url           VARCHAR(255) NOT NULL DEFAULT '' AFTER telegram_bot_username;
UPDATE branches SET
  telegram_bot_token    = COALESCE((SELECT value FROM company_settings WHERE `key` = 'telegram_bot_token'), ''),
  telegram_bot_username = COALESCE((SELECT value FROM company_settings WHERE `key` = 'telegram_bot_username'), ''),
  miniapp_url           = COALESCE((SELECT value FROM company_settings WHERE `key` = 'miniapp_url'), '');
UPDATE branches SET telegram_review_chat_id = COALESCE((SELECT CAST(value AS SIGNED) FROM company_settings WHERE `key` = 'telegram_review_chat_id'), 0)
WHERE telegram_review_chat_id = 0;
UPDATE branches SET telegram_group_id = COALESCE((SELECT CAST(value AS SIGNED) FROM company_settings WHERE `key` = 'telegram_group_id'), 0)
WHERE telegram_group_id = 0;

-- ---------- GPS: each branch has its own rules and office ----------
UPDATE branches SET
  gps_enabled        = COALESCE((SELECT value = 'true' FROM company_settings WHERE `key` = 'gps_enabled'), 0),
  gps_required       = COALESCE((SELECT value = 'true' FROM company_settings WHERE `key` = 'gps_required'), 0),
  gps_reject_outside = COALESCE((SELECT value = 'true' FROM company_settings WHERE `key` = 'gps_reject_outside'), 0)
WHERE gps_enabled IS NULL;
UPDATE branches SET
  office_latitude       = (SELECT CAST(value AS DECIMAL(10,7)) FROM company_settings WHERE `key` = 'office_latitude'),
  office_longitude      = (SELECT CAST(value AS DECIMAL(10,7)) FROM company_settings WHERE `key` = 'office_longitude'),
  allowed_radius_meters = (SELECT CAST(value AS DECIMAL(10,1)) FROM company_settings WHERE `key` = 'allowed_radius_meters')
WHERE office_latitude IS NULL
  AND EXISTS (SELECT 1 FROM company_settings WHERE `key` = 'office_latitude' AND value <> '' AND value <> '0')
  AND EXISTS (SELECT 1 FROM company_settings WHERE `key` = 'allowed_radius_meters' AND value <> '' AND value <> '0');

DELETE FROM company_settings WHERE `key` IN ('telegram_bot_token', 'telegram_bot_username', 'miniapp_url', 'telegram_group_id',
  'telegram_review_chat_id', 'gps_enabled', 'gps_required', 'gps_reject_outside', 'office_latitude', 'office_longitude', 'allowed_radius_meters');

-- ---------- roles ----------
-- Branch admins set up their own branch (shifts, holidays, leave types and balances).
INSERT IGNORE INTO role_permissions (role_id, permission) VALUES (5, 'schedules.manage'), (5, 'leave.manage');
-- Company Admin: runs the branches given to them (Branch assignment) or that they create.
SET @company_admin = GREATEST(6, (SELECT COALESCE(MAX(id), 0) + 1 FROM roles));
INSERT INTO roles (id, code, name, description, is_system)
SELECT @company_admin, 'company_admin', 'Company Admin', 'Sets up and runs the branches assigned to them or that they create', 1
FROM DUAL WHERE NOT EXISTS (SELECT 1 FROM roles WHERE code = 'company_admin');
INSERT IGNORE INTO role_permissions (role_id, permission)
SELECT r.id, p.permission FROM roles r CROSS JOIN (
  SELECT 'team.view' AS permission UNION ALL SELECT 'team.review' UNION ALL SELECT 'branch.view' UNION ALL SELECT 'branch.review'
  UNION ALL SELECT 'branch.settings' UNION ALL SELECT 'branches.create' UNION ALL SELECT 'staff.manage' UNION ALL SELECT 'attendance.edit'
  UNION ALL SELECT 'schedules.manage' UNION ALL SELECT 'leave.manage' UNION ALL SELECT 'report.export') p
WHERE r.code = 'company_admin';

-- ---------- the employee's reason for a check-in / check-out ----------
ALTER TABLE attendance
  ADD COLUMN check_in_reason  VARCHAR(255) NOT NULL DEFAULT '' AFTER check_out_at,
  ADD COLUMN check_out_reason VARCHAR(255) NOT NULL DEFAULT '' AFTER check_in_reason;
