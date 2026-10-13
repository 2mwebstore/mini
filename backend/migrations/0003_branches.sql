-- 0003: multiple branches, branch admins, and Telegram settings managed in the
-- admin panel instead of environment variables.

-- A branch admin manages exactly one branch; the owner (super admin) assigns them.
INSERT INTO roles (id, code, name, description) VALUES
  (5, 'branch_admin', 'Branch Admin', 'Manage one branch: staff, attendance, leave, reports');

CREATE TABLE branches (
  id                      BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
  code                    VARCHAR(32)  NOT NULL UNIQUE,
  name                    VARCHAR(150) NOT NULL,
  address                 VARCHAR(255) NOT NULL DEFAULT '',
  admin_employee_id       BIGINT UNSIGNED NULL UNIQUE,     -- one admin per branch, one branch per admin
  office_latitude         DOUBLE NULL,                     -- NULL = company default office location
  office_longitude        DOUBLE NULL,
  allowed_radius_meters   DOUBLE NULL,
  telegram_group_id       BIGINT NOT NULL DEFAULT 0,       -- this branch's daily report (0 = none)
  telegram_review_chat_id BIGINT NOT NULL DEFAULT 0,       -- review notifications (0 = DM the branch admin / manager)
  status                  VARCHAR(16)  NOT NULL DEFAULT 'ACTIVE',
  created_at              DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at              DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  CONSTRAINT fk_branches_admin FOREIGN KEY (admin_employee_id) REFERENCES employees(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Staff, managers and branch admins belong to a branch; super/HR admins are company-wide.
ALTER TABLE employees
  ADD COLUMN branch_id BIGINT UNSIGNED NULL AFTER department_id,
  ADD KEY idx_employees_branch (branch_id),
  ADD CONSTRAINT fk_employees_branch FOREIGN KEY (branch_id) REFERENCES branches(id);

-- Existing staff start in one default branch; rename it or add more in the admin panel.
INSERT INTO branches (code, name) VALUES ('MAIN', 'Main branch');
UPDATE employees SET branch_id = (SELECT id FROM (SELECT id FROM branches WHERE code = 'MAIN') b)
  WHERE role_id NOT IN (1, 2);

-- Telegram is configured in Settings (the bot token is stored encrypted).
INSERT INTO company_settings (`key`, value) VALUES
  ('telegram_bot_token', ''),
  ('telegram_bot_username', ''),
  ('miniapp_url', ''),
  ('telegram_group_id', '0'),
  ('telegram_review_chat_id', '0');
