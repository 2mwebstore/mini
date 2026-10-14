-- 0011: an employee can have several managers. Each of them reviews the person's
-- attendance and leave (Mini App Team tab) and gets their review messages.
CREATE TABLE employee_managers (
  employee_id BIGINT UNSIGNED NOT NULL,
  manager_id  BIGINT UNSIGNED NOT NULL,
  created_at  DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (employee_id, manager_id),
  KEY idx_employee_managers_manager (manager_id),
  CONSTRAINT fk_employee_managers_employee FOREIGN KEY (employee_id) REFERENCES employees(id),
  CONSTRAINT fk_employee_managers_manager FOREIGN KEY (manager_id) REFERENCES employees(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

INSERT INTO employee_managers (employee_id, manager_id)
SELECT id, manager_id FROM employees WHERE manager_id IS NOT NULL;

ALTER TABLE employees
  DROP FOREIGN KEY fk_employees_manager,
  DROP INDEX idx_employees_manager,
  DROP COLUMN manager_id;
