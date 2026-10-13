-- 0001_init: core schema for attendance & leave management.
-- All DATETIME columns are stored in UTC. work_date / leave dates are
-- calendar dates in the company timezone (company_settings.timezone).

CREATE TABLE roles (
  id          TINYINT UNSIGNED NOT NULL PRIMARY KEY,
  code        VARCHAR(32)  NOT NULL UNIQUE,
  name        VARCHAR(64)  NOT NULL,
  description VARCHAR(255) NOT NULL DEFAULT ''
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

INSERT INTO roles (id, code, name, description) VALUES
  (1, 'super_admin', 'Super Admin', 'Full access including settings and audit logs'),
  (2, 'hr_admin',    'HR / Admin',  'Manage staff, confirm attendance, approve leave, reports'),
  (3, 'manager',     'Manager',     'Manage and review assigned team'),
  (4, 'staff',       'Staff',       'Check in/out, request leave, view own data');

CREATE TABLE departments (
  id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
  name       VARCHAR(100) NOT NULL UNIQUE,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE work_schedules (
  id                BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
  name              VARCHAR(100) NOT NULL UNIQUE,
  start_time        CHAR(5) NOT NULL,          -- HH:MM local time
  end_time          CHAR(5) NOT NULL,          -- HH:MM local time
  grace_minutes     INT NOT NULL DEFAULT 0,
  work_days         VARCHAR(20) NOT NULL DEFAULT '1,2,3,4,5', -- ISO weekdays, 1=Mon .. 7=Sun
  is_default        TINYINT(1) NOT NULL DEFAULT 0,
  created_at        DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at        DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

INSERT INTO work_schedules (name, start_time, end_time, grace_minutes, work_days, is_default)
VALUES ('Office Hours', '09:00', '17:30', 10, '1,2,3,4,5', 1);

CREATE TABLE employees (
  id               BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
  employee_code    VARCHAR(32)  NOT NULL UNIQUE,
  full_name        VARCHAR(150) NOT NULL,
  department_id    BIGINT UNSIGNED NULL,
  position         VARCHAR(100) NOT NULL DEFAULT '',
  phone            VARCHAR(32)  NOT NULL DEFAULT '',
  email            VARCHAR(150) NOT NULL DEFAULT '',
  join_date        DATE NULL,
  work_schedule_id BIGINT UNSIGNED NULL,
  manager_id       BIGINT UNSIGNED NULL,
  role_id          TINYINT UNSIGNED NOT NULL DEFAULT 4,
  status           VARCHAR(16) NOT NULL DEFAULT 'ACTIVE',  -- ACTIVE | INACTIVE | SUSPENDED
  created_at       DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at       DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  deleted_at       DATETIME(3) NULL,
  KEY idx_employees_status (status),
  KEY idx_employees_manager (manager_id),
  KEY idx_employees_department (department_id),
  CONSTRAINT fk_employees_department FOREIGN KEY (department_id) REFERENCES departments(id),
  CONSTRAINT fk_employees_schedule   FOREIGN KEY (work_schedule_id) REFERENCES work_schedules(id),
  CONSTRAINT fk_employees_manager    FOREIGN KEY (manager_id) REFERENCES employees(id),
  CONSTRAINT fk_employees_role       FOREIGN KEY (role_id) REFERENCES roles(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Telegram User ID is the identity; username is informational only (it can change).
CREATE TABLE telegram_accounts (
  id               BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
  employee_id      BIGINT UNSIGNED NOT NULL UNIQUE,
  telegram_user_id BIGINT NOT NULL UNIQUE,
  username         VARCHAR(64)  NOT NULL DEFAULT '',
  first_name       VARCHAR(128) NOT NULL DEFAULT '',
  last_name        VARCHAR(128) NOT NULL DEFAULT '',
  linked_at        DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at       DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  CONSTRAINT fk_tg_employee FOREIGN KEY (employee_id) REFERENCES employees(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Dashboard logins. Every user is an employee, so the role lives in one place.
CREATE TABLE users (
  id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
  employee_id   BIGINT UNSIGNED NOT NULL UNIQUE,
  email         VARCHAR(150) NOT NULL UNIQUE,
  password_hash VARCHAR(255) NOT NULL,
  status        VARCHAR(16)  NOT NULL DEFAULT 'ACTIVE',
  last_login_at DATETIME(3) NULL,
  created_at    DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at    DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  CONSTRAINT fk_users_employee FOREIGN KEY (employee_id) REFERENCES employees(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE holidays (
  id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
  date       DATE NOT NULL UNIQUE,
  name       VARCHAR(150) NOT NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE attendance (
  id                  BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
  employee_id         BIGINT UNSIGNED NOT NULL,
  work_date           DATE NOT NULL,
  check_in_at         DATETIME(3) NULL,
  check_out_at        DATETIME(3) NULL,
  working_minutes     INT NOT NULL DEFAULT 0,
  late_minutes        INT NOT NULL DEFAULT 0,
  early_leave_minutes INT NOT NULL DEFAULT 0,
  status              VARCHAR(16) NOT NULL,          -- PRESENT LATE EARLY_LEAVE ABSENT LEAVE HOLIDAY WEEKEND
  confirmation_status VARCHAR(16) NOT NULL,          -- PENDING CONFIRMED REJECTED
  confirmed_by        BIGINT UNSIGNED NULL,
  confirmed_at        DATETIME(3) NULL,
  source              VARCHAR(16) NOT NULL DEFAULT 'TELEGRAM', -- TELEGRAM MANUAL SYSTEM
  note                VARCHAR(500) NOT NULL DEFAULT '',
  created_at          DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at          DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  UNIQUE KEY uq_attendance_employee_day (employee_id, work_date),
  KEY idx_attendance_date_status (work_date, status),
  KEY idx_attendance_confirmation (confirmation_status),
  CONSTRAINT fk_attendance_employee  FOREIGN KEY (employee_id)  REFERENCES employees(id),
  CONSTRAINT fk_attendance_confirmer FOREIGN KEY (confirmed_by) REFERENCES employees(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE attendance_photos (
  id                      BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
  attendance_id           BIGINT UNSIGNED NOT NULL,
  type                    VARCHAR(16) NOT NULL,      -- CHECK_IN | CHECK_OUT
  telegram_file_id        VARCHAR(255) NOT NULL,
  telegram_file_unique_id VARCHAR(128) NOT NULL DEFAULT '',
  telegram_user_id        BIGINT NOT NULL,
  storage_key             VARCHAR(500) NOT NULL,
  storage_url             VARCHAR(1000) NOT NULL DEFAULT '',
  content_type            VARCHAR(64) NOT NULL DEFAULT '',
  size_bytes              BIGINT NOT NULL DEFAULT 0,
  created_at              DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  KEY idx_photos_attendance (attendance_id, type),
  KEY idx_photos_unique (telegram_file_unique_id),
  CONSTRAINT fk_photos_attendance FOREIGN KEY (attendance_id) REFERENCES attendance(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE attendance_locations (
  id              BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
  attendance_id   BIGINT UNSIGNED NOT NULL,
  type            VARCHAR(16) NOT NULL,
  latitude        DECIMAL(10,7) NOT NULL,
  longitude       DECIMAL(10,7) NOT NULL,
  accuracy        DOUBLE NULL,
  distance_meters DOUBLE NULL,
  inside_office   TINYINT(1) NULL,
  created_at      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  KEY idx_locations_attendance (attendance_id, type),
  CONSTRAINT fk_locations_attendance FOREIGN KEY (attendance_id) REFERENCES attendance(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- One review per check-in and per check-out. attendance.confirmation_status is derived from these.
CREATE TABLE attendance_confirmations (
  id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
  attendance_id BIGINT UNSIGNED NOT NULL,
  type          VARCHAR(16) NOT NULL,       -- CHECK_IN | CHECK_OUT
  status        VARCHAR(16) NOT NULL,       -- PENDING | CONFIRMED | REJECTED | SUPERSEDED
  reviewed_by   BIGINT UNSIGNED NULL,
  reviewed_at   DATETIME(3) NULL,
  reason        VARCHAR(500) NOT NULL DEFAULT '',
  created_at    DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at    DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  KEY idx_conf_attendance (attendance_id, type),
  KEY idx_conf_status (status),
  CONSTRAINT fk_conf_attendance FOREIGN KEY (attendance_id) REFERENCES attendance(id),
  CONSTRAINT fk_conf_reviewer   FOREIGN KEY (reviewed_by)   REFERENCES employees(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE leave_types (
  id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
  code       VARCHAR(32)  NOT NULL UNIQUE,
  name       VARCHAR(100) NOT NULL,
  active     TINYINT(1) NOT NULL DEFAULT 1,
  sort_order INT NOT NULL DEFAULT 0
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

INSERT INTO leave_types (code, name, sort_order) VALUES
  ('ANNUAL', 'Annual Leave', 1), ('SICK', 'Sick Leave', 2), ('PERSONAL', 'Personal Leave', 3),
  ('EMERGENCY', 'Emergency Leave', 4), ('OTHER', 'Other', 5);

CREATE TABLE leave_requests (
  id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
  employee_id   BIGINT UNSIGNED NOT NULL,
  leave_type_id BIGINT UNSIGNED NOT NULL,
  start_date    DATE NOT NULL,
  end_date      DATE NOT NULL,
  days          INT NOT NULL DEFAULT 0,      -- working days in range
  reason        VARCHAR(1000) NOT NULL DEFAULT '',
  status        VARCHAR(16) NOT NULL DEFAULT 'PENDING', -- PENDING APPROVED REJECTED CANCELLED
  reviewed_by   BIGINT UNSIGNED NULL,
  reviewed_at   DATETIME(3) NULL,
  review_note   VARCHAR(500) NOT NULL DEFAULT '',
  created_at    DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at    DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  KEY idx_leave_employee_dates (employee_id, start_date, end_date),
  KEY idx_leave_status (status),
  CONSTRAINT fk_leave_employee FOREIGN KEY (employee_id)   REFERENCES employees(id),
  CONSTRAINT fk_leave_type     FOREIGN KEY (leave_type_id) REFERENCES leave_types(id),
  CONSTRAINT fk_leave_reviewer FOREIGN KEY (reviewed_by)   REFERENCES employees(id),
  CONSTRAINT chk_leave_range CHECK (end_date >= start_date)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE audit_logs (
  id                 BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
  actor_employee_id  BIGINT UNSIGNED NULL,
  channel            VARCHAR(16) NOT NULL,   -- TELEGRAM DASHBOARD SYSTEM CLI
  action             VARCHAR(64) NOT NULL,
  entity_type        VARCHAR(32) NOT NULL DEFAULT '',
  entity_id          BIGINT UNSIGNED NULL,
  target_employee_id BIGINT UNSIGNED NULL,
  details            JSON NULL,
  ip                 VARCHAR(64) NOT NULL DEFAULT '',
  created_at         DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  KEY idx_audit_action (action),
  KEY idx_audit_created (created_at),
  KEY idx_audit_target (target_employee_id),
  KEY idx_audit_entity (entity_type, entity_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Audit logs are append-only at the database level, not just in application code.
CREATE TRIGGER trg_audit_logs_no_update BEFORE UPDATE ON audit_logs FOR EACH ROW
  SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'audit_logs is append-only';

CREATE TRIGGER trg_audit_logs_no_delete BEFORE DELETE ON audit_logs FOR EACH ROW
  SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'audit_logs is append-only';

CREATE TABLE notifications (
  id                    BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
  recipient_chat_id     BIGINT NOT NULL,
  recipient_employee_id BIGINT UNSIGNED NULL,
  type                  VARCHAR(48) NOT NULL,
  ref_type              VARCHAR(32) NOT NULL DEFAULT '',
  ref_id                BIGINT UNSIGNED NULL,
  status                VARCHAR(16) NOT NULL,   -- SENT | FAILED
  telegram_message_id   BIGINT NULL,
  error                 VARCHAR(500) NOT NULL DEFAULT '',
  created_at            DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  KEY idx_notifications_ref (ref_type, ref_id),
  KEY idx_notifications_created (created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE company_settings (
  `key`      VARCHAR(64) NOT NULL PRIMARY KEY,
  value      VARCHAR(500) NOT NULL,
  updated_by BIGINT UNSIGNED NULL,
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

INSERT INTO company_settings (`key`, value) VALUES
  ('company_name', 'My Company'),
  ('timezone', 'Asia/Phnom_Penh'),
  ('confirmation_required', 'true'),
  ('gps_enabled', 'false'),
  ('gps_required', 'false'),
  ('gps_reject_outside', 'false'),
  ('office_latitude', '11.5564'),
  ('office_longitude', '104.9282'),
  ('allowed_radius_meters', '100'),
  ('daily_report_enabled', 'true'),
  ('daily_report_time', '18:00'),
  ('missing_checkin_alert_time', '10:00'),
  ('day_close_time', '23:50');
