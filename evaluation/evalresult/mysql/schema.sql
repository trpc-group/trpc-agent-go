-- MySQL Evaluation Result Schema.
-- This file provides reference SQL for manual database initialization.
-- The manager will automatically create these tables if skipDBInit is false.
-- Note: Replace {{PREFIX}} with your actual table prefix (e.g., trpc_) in table names.

CREATE TABLE IF NOT EXISTS `{{PREFIX}}evaluation_eval_set_results` (
  `id` BIGINT NOT NULL AUTO_INCREMENT,
  `app_name` VARCHAR(255) NOT NULL,
  `eval_set_result_id` VARCHAR(255) NOT NULL,
  `eval_set_id` VARCHAR(255) NOT NULL,
  `eval_set_result_name` VARCHAR(255) NOT NULL,
  `eval_case_results` JSON NOT NULL,
  `summary` JSON DEFAULT NULL,
  `created_at` TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  `updated_at` TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
  PRIMARY KEY (`id`),
  UNIQUE KEY `uniq_results_app_result_id` (`app_name`, `eval_set_result_id`),
  KEY `idx_results_app_created` (`app_name`, `created_at`),
  KEY `idx_results_app_set_created` (`app_name`, `eval_set_id`, `created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
