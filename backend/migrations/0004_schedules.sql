-- 0004: shift work types (normal / cross-day night shifts), the weekly roster and
-- special (temporary) schedules.
--
-- What an employee works on a date: a special schedule covering the date wins,
-- then the roster entry for that date, then their usual shift's weekly pattern.

-- NORMAL ends the same day (08:00-17:00); CROSS_DAY ends the next day (22:00-06:00).
ALTER TABLE work_schedules
  ADD COLUMN work_type VARCHAR(16) NOT NULL DEFAULT 'NORMAL' AFTER end_time,
  ADD COLUMN color     CHAR(7)     NOT NULL DEFAULT '#2563eb' AFTER work_days;
UPDATE work_schedules SET work_type = 'CROSS_DAY' WHERE end_time <= start_time;

-- Weekly roster: the shift an employee works on one date. NULL shift = day off.
CREATE TABLE roster_shifts (
  id               BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
  employee_id      BIGINT UNSIGNED NOT NULL,
  work_date        DATE NOT NULL,
  work_schedule_id BIGINT UNSIGNED NULL,
  updated_by       BIGINT UNSIGNED NULL,
  created_at       DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at       DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  UNIQUE KEY uq_roster_employee_date (employee_id, work_date),
  KEY idx_roster_date (work_date),
  CONSTRAINT fk_roster_employee FOREIGN KEY (employee_id) REFERENCES employees(id),
  CONSTRAINT fk_roster_schedule FOREIGN KEY (work_schedule_id) REFERENCES work_schedules(id),
  CONSTRAINT fk_roster_updated_by FOREIGN KEY (updated_by) REFERENCES employees(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Special / temporary schedule: for start_date..end_date the employee works
--   SHIFT  another shift (work_schedule_id),
--   CUSTOM their own hours (start_time, end_time, work_type, grace_minutes), or
--   OFF    not at all,
-- on the ISO weekdays in work_days (1=Mon .. 7=Sun). One employee's ranges never overlap.
CREATE TABLE schedule_overrides (
  id               BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
  employee_id      BIGINT UNSIGNED NOT NULL,
  start_date       DATE NOT NULL,
  end_date         DATE NOT NULL,
  kind             VARCHAR(16) NOT NULL,
  work_schedule_id BIGINT UNSIGNED NULL,
  start_time       CHAR(5) NULL,
  end_time         CHAR(5) NULL,
  work_type        VARCHAR(16) NULL,
  grace_minutes    INT NULL,
  work_days        VARCHAR(20) NOT NULL DEFAULT '1,2,3,4,5,6,7',
  reason           VARCHAR(255) NOT NULL DEFAULT '',
  created_by       BIGINT UNSIGNED NULL,
  created_at       DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at       DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  KEY idx_override_employee (employee_id, start_date, end_date),
  KEY idx_override_dates (start_date, end_date),
  CONSTRAINT fk_override_employee FOREIGN KEY (employee_id) REFERENCES employees(id),
  CONSTRAINT fk_override_schedule FOREIGN KEY (work_schedule_id) REFERENCES work_schedules(id),
  CONSTRAINT fk_override_created_by FOREIGN KEY (created_by) REFERENCES employees(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
