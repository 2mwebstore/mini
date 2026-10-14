-- 0005: role management (each role's permissions live here, not in code), branch
-- access for system users, managed departments/positions, leave types with yearly
-- allowances and approval policies, leave balances, and GPS rules per branch.

-- ---------- roles & permissions ----------
-- Built-in roles are "system" roles (cannot be deleted); new roles get ids from 6.
ALTER TABLE roles
  ADD COLUMN is_system  TINYINT(1)  NOT NULL DEFAULT 0,
  ADD COLUMN created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  ADD COLUMN updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3);
UPDATE roles SET is_system = 1;

CREATE TABLE role_permissions (
  role_id    TINYINT UNSIGNED NOT NULL,
  permission VARCHAR(64) NOT NULL,
  PRIMARY KEY (role_id, permission),
  CONSTRAINT fk_role_permissions_role FOREIGN KEY (role_id) REFERENCES roles(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- What the built-in roles could do before (the owner always has every permission).
INSERT INTO role_permissions (role_id, permission) VALUES
  (4, 'attendance.self'), (4, 'leave.self'),
  (3, 'attendance.self'), (3, 'leave.self'), (3, 'team.view'), (3, 'team.review'), (3, 'report.export'),
  (2, 'attendance.self'), (2, 'leave.self'), (2, 'team.view'), (2, 'team.review'), (2, 'all.view'), (2, 'all.review'),
  (2, 'attendance.edit'), (2, 'staff.manage'), (2, 'report.export'), (2, 'settings.view'), (2, 'schedules.manage'), (2, 'leave.manage'),
  (5, 'attendance.self'), (5, 'leave.self'), (5, 'team.view'), (5, 'team.review'), (5, 'branch.view'), (5, 'branch.review'),
  (5, 'attendance.edit'), (5, 'staff.manage'), (5, 'report.export'), (5, 'branch.settings');

-- ---------- branch access for system users ----------
-- A user whose role sees everyone, with no rows here, reaches every branch; with
-- rows, only those branches. A branch admin always reaches their own branch too.
CREATE TABLE user_branches (
  employee_id BIGINT UNSIGNED NOT NULL,
  branch_id   BIGINT UNSIGNED NOT NULL,
  created_at  DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (employee_id, branch_id),
  KEY idx_user_branches_branch (branch_id),
  CONSTRAINT fk_user_branches_employee FOREIGN KEY (employee_id) REFERENCES employees(id),
  CONSTRAINT fk_user_branches_branch FOREIGN KEY (branch_id) REFERENCES branches(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ---------- positions (managed list; employees.position keeps the name) ----------
CREATE TABLE positions (
  id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
  name       VARCHAR(100) NOT NULL UNIQUE,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
INSERT INTO positions (name) SELECT DISTINCT TRIM(position) FROM employees WHERE TRIM(position) <> '';
ALTER TABLE employees
  ADD COLUMN position_id BIGINT UNSIGNED NULL AFTER position,
  ADD CONSTRAINT fk_employees_position FOREIGN KEY (position_id) REFERENCES positions(id);
UPDATE employees e JOIN positions p ON p.name = TRIM(e.position) SET e.position_id = p.id;

-- ---------- leave types: allowance and policy ----------
ALTER TABLE leave_types
  ADD COLUMN annual_days          DECIMAL(5,1) NOT NULL DEFAULT 0 AFTER name, -- yearly allowance; 0 = not counted
  ADD COLUMN is_paid              TINYINT(1)   NOT NULL DEFAULT 1,
  ADD COLUMN approval             VARCHAR(16)  NOT NULL DEFAULT 'MANUAL',     -- MANUAL (a reviewer approves) | AUTO
  ADD COLUMN allow_exceed         TINYINT(1)   NOT NULL DEFAULT 0,            -- may ask for more than the balance
  ADD COLUMN max_days_per_request INT          NOT NULL DEFAULT 0,            -- 0 = no limit
  ADD COLUMN min_notice_days      INT          NOT NULL DEFAULT 0,            -- days before the start date
  ADD COLUMN color                CHAR(7)      NOT NULL DEFAULT '#2563eb',
  ADD COLUMN description          VARCHAR(255) NOT NULL DEFAULT '',
  ADD COLUMN created_at           DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  ADD COLUMN updated_at           DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3);
UPDATE leave_types SET annual_days = 18, color = '#16a34a' WHERE code = 'ANNUAL';
UPDATE leave_types SET annual_days = 7, allow_exceed = 1, color = '#dc2626' WHERE code = 'SICK';
UPDATE leave_types SET annual_days = 5, color = '#7c3aed' WHERE code = 'PERSONAL';
UPDATE leave_types SET annual_days = 3, allow_exceed = 1, color = '#ea580c' WHERE code = 'EMERGENCY';
UPDATE leave_types SET is_paid = 0, color = '#64748b' WHERE code = 'OTHER';

-- ---------- leave balances ----------
-- Only where an employee's entitlement differs from the type's allowance (or has a
-- carry-over). Used / pending days are counted from leave requests.
CREATE TABLE leave_balances (
  employee_id   BIGINT UNSIGNED  NOT NULL,
  leave_type_id BIGINT UNSIGNED  NOT NULL,
  year          SMALLINT UNSIGNED NOT NULL,
  allocated     DECIMAL(5,1) NOT NULL,
  carried       DECIMAL(5,1) NOT NULL DEFAULT 0,
  note          VARCHAR(255) NOT NULL DEFAULT '',
  updated_by    BIGINT UNSIGNED NULL,
  updated_at    DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (employee_id, leave_type_id, year),
  CONSTRAINT fk_leave_balances_employee FOREIGN KEY (employee_id) REFERENCES employees(id),
  CONSTRAINT fk_leave_balances_type FOREIGN KEY (leave_type_id) REFERENCES leave_types(id),
  CONSTRAINT fk_leave_balances_updated_by FOREIGN KEY (updated_by) REFERENCES employees(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ---------- GPS rules per branch (NULL = the company setting) ----------
ALTER TABLE branches
  ADD COLUMN gps_enabled        TINYINT(1) NULL AFTER allowed_radius_meters,
  ADD COLUMN gps_required       TINYINT(1) NULL AFTER gps_enabled,
  ADD COLUMN gps_reject_outside TINYINT(1) NULL AFTER gps_required;
