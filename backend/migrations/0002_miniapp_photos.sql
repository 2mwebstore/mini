-- Telegram Mini App: photos are uploaded directly instead of arriving as a
-- Telegram file_id (telegram_file_id is filled in once the first reviewer
-- notification has sent the photo). content_sha256 blocks re-using the same
-- image from any channel.
ALTER TABLE attendance_photos
  ADD COLUMN content_sha256 CHAR(64) NOT NULL DEFAULT '' AFTER telegram_file_unique_id,
  ADD KEY idx_photos_sha256 (content_sha256);
