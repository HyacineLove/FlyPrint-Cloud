-- Persist explicit page direction and user-selected print scale.
ALTER TABLE print_jobs
    ADD COLUMN IF NOT EXISTS orientation VARCHAR(16) NOT NULL DEFAULT 'portrait';

ALTER TABLE print_jobs
    ADD COLUMN IF NOT EXISTS scale_percent INTEGER NOT NULL DEFAULT 100;

UPDATE print_jobs
SET orientation = 'portrait'
WHERE orientation IS NULL OR orientation NOT IN ('portrait', 'landscape');

UPDATE print_jobs
SET scale_percent = 100
WHERE scale_percent IS NULL
   OR scale_percent < 50
   OR scale_percent > 150
   OR MOD(scale_percent, 10) <> 0;
