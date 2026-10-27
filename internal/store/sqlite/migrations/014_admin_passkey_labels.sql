-- An administrator lists their own passkeys and removes one they lost
-- (UI-008). The label names where the passkey was made ("Chrome on Linux"),
-- derived from the browser's User-Agent at creation; older rows have none.
ALTER TABLE admin_passkeys ADD COLUMN label TEXT CHECK (label IS NULL OR length(label) BETWEEN 1 AND 60);
