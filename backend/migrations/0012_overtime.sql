-- 0012: overtime requests. An employee asks for extra hours on a date (by default
-- from the end of their shift, for an hour), with a reason; one of their reviewers
-- (manager, branch admin, HR, owner) approves or rejects it; the employee can cancel
-- it while it is pending (or approved and not started). Reports count pending and
-- approved overtime.
CREATE TABLE overtime_requests (
  id          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
  employee_id BIGINT UNSIGNED NOT NULL,
  work_date   DATE NOT NULL,          -- the day it belongs to (company time)
  start_at    DATETIME(3) NOT NULL,   -- UTC
  end_at      DATETIME(3) NOT NULL,   -- UTC; may be the next day
  minutes     INT NOT NULL,
  reason      VARCHAR(500) NOT NULL,
  status      VARCHAR(16) NOT NULL DEFAULT 'PENDING', -- PENDING | APPROVED | REJECTED | CANCELLED
  reviewed_by BIGINT UNSIGNED NULL,
  reviewed_at DATETIME(3) NULL,
  review_note VARCHAR(500) NOT NULL DEFAULT '',
  created_at  DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at  DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  KEY idx_overtime_employee_date (employee_id, work_date),
  KEY idx_overtime_status (status),
  CONSTRAINT fk_overtime_employee FOREIGN KEY (employee_id) REFERENCES employees(id),
  CONSTRAINT fk_overtime_reviewer FOREIGN KEY (reviewed_by) REFERENCES employees(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
