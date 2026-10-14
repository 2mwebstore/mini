-- 0006: half-day leave, and check-ins without a photo.
-- A leave request is a full day (or range of days), or one half of a single working
-- day; the half's hours come from that day's shift (normal or cross-day) and are
-- kept on the request as they were when it was made.
ALTER TABLE leave_requests
  MODIFY COLUMN days DECIMAL(5,1) NOT NULL DEFAULT 0,                          -- working days; 0.5 for a half day
  ADD COLUMN day_type  VARCHAR(16) NOT NULL DEFAULT 'FULL' AFTER end_date,     -- FULL | FIRST_HALF | SECOND_HALF
  ADD COLUMN half_from CHAR(5) NULL AFTER day_type,                            -- HH:MM, the half's hours (company time)
  ADD COLUMN half_to   CHAR(5) NULL AFTER half_from;

-- Each check-in/out submission keeps its own location (and photo, for records made
-- while the app still took one), so a re-submission is never shown with the
-- previous one's. Old rows: the latest of that type recorded with the submission.
ALTER TABLE attendance_confirmations
  ADD COLUMN photo_id    BIGINT UNSIGNED NULL AFTER type,
  ADD COLUMN location_id BIGINT UNSIGNED NULL AFTER photo_id;
UPDATE attendance_confirmations c SET
  photo_id = (SELECT MAX(p.id) FROM attendance_photos p
              WHERE p.attendance_id = c.attendance_id AND p.type = c.type AND p.created_at <= c.created_at + INTERVAL 5 SECOND),
  location_id = (SELECT MAX(l.id) FROM attendance_locations l
                 WHERE l.attendance_id = c.attendance_id AND l.type = c.type AND l.created_at <= c.created_at + INTERVAL 5 SECOND);
