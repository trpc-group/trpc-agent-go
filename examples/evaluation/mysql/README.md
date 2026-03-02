# MySQL Evaluation Example

This example runs the evaluation pipeline with MySQL-backed managers for EvalSet, Metric, and EvalResult.

## Prerequisites

- A reachable MySQL server with JSON column support.
- A database created ahead of time.
- If `-skip-db-init=false`, the MySQL user needs permissions to create tables.

## Environment Variables

| Variable | Description | Default Value |
|----------|-------------|---------------|
| `OPENAI_API_KEY` | API key for the model service (required) | `` |
| `OPENAI_BASE_URL` | Base URL for the model API endpoint | `https://api.openai.com/v1` |

## Configuration Flags

| Flag | Description | Default |
|------|-------------|---------|
| `-dsn` | MySQL DSN used by evaluation managers | `user:password@tcp(localhost:3306)/db?parseTime=true&charset=utf8mb4` |
| `-table-prefix` | Table prefix for all evaluation tables | `evaluation_example` |
| `-skip-db-init` | Skip table creation during manager initialization | `false` |
| `-eval-set` | Evaluation set ID to execute | `math-basic` |
| `-runs` | Number of repetitions per evaluation case | `1` |
| `-model` | Model identifier used by the calculator agent | `deepseek-chat` |
| `-streaming` | Enable streaming responses from the LLM | `false` |

## Database Setup

The example expects an EvalSet and Metric definition to already exist in MySQL.

The SQL below creates the required tables and inserts a minimal dataset that matches the defaults:

- `appName`: `math-eval-app`
- `evalSetId`: `math-basic`
- `table-prefix`: `evaluation_example`

Replace `{{PREFIX}}` with your table prefix. For the default flags, use `evaluation_example_`.

```sql
CREATE TABLE IF NOT EXISTS `{{PREFIX}}evaluation_eval_sets` (
  `id` BIGINT NOT NULL AUTO_INCREMENT,
  `app_name` VARCHAR(255) NOT NULL,
  `eval_set_id` VARCHAR(255) NOT NULL,
  `name` VARCHAR(255) NOT NULL,
  `description` TEXT DEFAULT NULL,
  `created_at` TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  `updated_at` TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
  PRIMARY KEY (`id`),
  UNIQUE KEY `uniq_eval_sets_app_eval_set` (`app_name`, `eval_set_id`),
  KEY `idx_eval_sets_app_created` (`app_name`, `created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `{{PREFIX}}evaluation_eval_cases` (
  `id` BIGINT NOT NULL AUTO_INCREMENT,
  `app_name` VARCHAR(255) NOT NULL,
  `eval_set_id` VARCHAR(255) NOT NULL,
  `eval_id` VARCHAR(255) NOT NULL,
  `eval_mode` VARCHAR(32) NOT NULL DEFAULT '',
  `eval_case` JSON NOT NULL,
  `created_at` TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  `updated_at` TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
  PRIMARY KEY (`id`),
  UNIQUE KEY `uniq_eval_cases_app_set_case` (`app_name`, `eval_set_id`, `eval_id`),
  KEY `idx_eval_cases_app_set_order` (`app_name`, `eval_set_id`, `id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `{{PREFIX}}evaluation_metrics` (
  `id` BIGINT NOT NULL AUTO_INCREMENT,
  `app_name` VARCHAR(255) NOT NULL,
  `eval_set_id` VARCHAR(255) NOT NULL,
  `metric_name` VARCHAR(255) NOT NULL,
  `metric` JSON NOT NULL,
  `created_at` TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  `updated_at` TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
  PRIMARY KEY (`id`),
  UNIQUE KEY `uniq_metrics_app_set_name` (`app_name`, `eval_set_id`, `metric_name`),
  KEY `idx_metrics_app_set` (`app_name`, `eval_set_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

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

INSERT INTO `{{PREFIX}}evaluation_eval_sets` (`app_name`, `eval_set_id`, `name`, `description`)
VALUES ('math-eval-app', 'math-basic', 'math-basic', '')
ON DUPLICATE KEY UPDATE `name` = VALUES(`name`), `description` = VALUES(`description`);

INSERT INTO `{{PREFIX}}evaluation_eval_cases` (`app_name`, `eval_set_id`, `eval_id`, `eval_mode`, `eval_case`)
VALUES
  ('math-eval-app', 'math-basic', 'calc_add', '', CAST('{
    "evalId": "calc_add",
    "conversation": [
      {
        "invocationId": "calc_add-1",
        "userContent": {"role": "user", "content": "calc add 2 3"},
        "finalResponse": {"role": "assistant", "content": "calc result: 5"},
        "tools": [
          {
            "id": "tool_use_1",
            "name": "calculator",
            "arguments": {"operation": "add", "a": 2, "b": 3},
            "result": {"a": 2, "b": 3, "operation": "add", "result": 5}
          }
        ]
      }
    ],
    "sessionInput": {"appName": "math-eval-app", "userId": "user"}
  }' AS JSON)),
  ('math-eval-app', 'math-basic', 'calc_multiply', '', CAST('{
    "evalId": "calc_multiply",
    "conversation": [
      {
        "invocationId": "calc_multiply-1",
        "userContent": {"role": "user", "content": "calc multiply 6 7"},
        "finalResponse": {"role": "assistant", "content": "calc result: 42"},
        "tools": [
          {
            "id": "tool_use_2",
            "name": "calculator",
            "arguments": {"operation": "multiply", "a": 6, "b": 7},
            "result": {"a": 6, "b": 7, "operation": "multiply", "result": 42}
          }
        ]
      }
    ],
    "sessionInput": {"appName": "math-eval-app", "userId": "user"}
  }' AS JSON))
ON DUPLICATE KEY UPDATE
  `eval_mode` = VALUES(`eval_mode`),
  `eval_case` = VALUES(`eval_case`),
  `updated_at` = CURRENT_TIMESTAMP(6);

INSERT INTO `{{PREFIX}}evaluation_metrics` (`app_name`, `eval_set_id`, `metric_name`, `metric`)
VALUES (
  'math-eval-app',
  'math-basic',
  'tool_trajectory_avg_score',
  CAST('{
    "metricName": "tool_trajectory_avg_score",
    "threshold": 1,
    "criterion": {
      "toolTrajectory": {
        "orderSensitive": false,
        "defaultStrategy": {
          "name": {"matchStrategy": "exact"},
          "arguments": {"matchStrategy": "exact"},
          "result": {"matchStrategy": "exact"}
        }
      }
    }
  }' AS JSON)
)
ON DUPLICATE KEY UPDATE
  `metric` = VALUES(`metric`),
  `updated_at` = CURRENT_TIMESTAMP(6);
```

## Run

```bash
cd trpc-agent-go/examples/evaluation/mysql
go run . \
  -dsn "user:password@tcp(localhost:3306)/trpc_evaluation?parseTime=true&charset=utf8mb4" \
  -table-prefix "evaluation_example" \
  -eval-set "math-basic" \
  -runs 1
```

It prints a case-by-case summary and the EvalSetResult ID saved in MySQL.

## Verify

After a successful run, the example writes a new row into `{{PREFIX}}evaluation_eval_set_results`.

```sql
SELECT `eval_set_result_id`, `eval_set_id`, `created_at`
FROM `{{PREFIX}}evaluation_eval_set_results`
WHERE `app_name` = 'math-eval-app'
ORDER BY `created_at` DESC
LIMIT 5;
```

## Notes

- The DSN should include `parseTime=true` so that MySQL timestamp columns scan correctly.
- The SQL table definitions match:
  - `evaluation/evalset/mysql/schema.sql`
  - `evaluation/metric/mysql/schema.sql`
  - `evaluation/evalresult/mysql/schema.sql`
