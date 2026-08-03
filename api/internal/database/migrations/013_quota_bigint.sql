-- 额度相关列 INTEGER → BIGINT：避免大额 grant/累计/退款触发 32 位溢出
ALTER TABLE users ALTER COLUMN print_quota_balance TYPE BIGINT;
ALTER TABLE print_jobs ALTER COLUMN quota_reserved TYPE BIGINT;
ALTER TABLE print_jobs ALTER COLUMN quota_consumed TYPE BIGINT;
ALTER TABLE print_jobs ALTER COLUMN impressions_completed TYPE BIGINT;
ALTER TABLE print_jobs ALTER COLUMN sheets_completed TYPE BIGINT;
ALTER TABLE print_quota_transactions ALTER COLUMN delta TYPE BIGINT;
ALTER TABLE print_quota_transactions ALTER COLUMN balance_after TYPE BIGINT;
