-- MySQL PromptIter Store Schema.
-- This file provides reference SQL for manual database initialization.
-- The store will automatically create this table if skipDBInit is false.
-- Note: Replace {{PREFIX}} with your actual table prefix (e.g., trpc_) in table names.

CREATE TABLE IF NOT EXISTS `{{PREFIX}}promptiter_runs` (
  `id` BIGINT NOT NULL AUTO_INCREMENT,
  `run_id` VARCHAR(255) NOT NULL,
  `status` VARCHAR(32) NOT NULL DEFAULT '',
  `run_result` JSON NOT NULL,
  `created_at` TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  `updated_at` TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
  PRIMARY KEY (`id`),
  UNIQUE KEY `uniq_promptiter_runs_run_id` (`run_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
