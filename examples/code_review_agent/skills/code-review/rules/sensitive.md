# Sensitive Information Rules

## SEN-001: Hardcoded API Key or Token in Assignment

- **type**: token
- **severity**: critical
- **pattern**: `(?i)(api[_-]?key|api[_-]?secret|api[_-]?token|token|secret[_-]?key)\s*[:=]\s*["'][A-Za-z0-9_\-]{12,}["']`
- **message**: "硬编码凭据赋值。应移到环境变量或配置文件"
- **fix**: "使用 os.Getenv(\"SECRET_NAME\") 从环境变量读取"

## SEN-002: Hardcoded Password

- **type**: token
- **severity**: critical
- **pattern**: `(?i)(password|passwd|pwd)\s*[:=]\s*["'][A-Za-z0-9_\-!@#$%^&*()]{6,}["']`
- **message**: "硬编码密码。密码绝不应该出现在源代码中"
- **fix**: "从环境变量、密钥管理服务或加密配置中读取密码"

## SEN-003: Private Key in Source Code

- **type**: token
- **severity**: critical
- **pattern**: `(?i)-----BEGIN\s+(RSA\s+|EC\s+|OPENSSH\s+)?PRIVATE\s+KEY-----`
- **message**: "源代码中包含私钥。私钥绝不应出现在代码中"
- **fix**: "使用密钥管理服务 (KMS) 或环境变量注入"
