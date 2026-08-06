# SI-001 - Hardcoded Sensitive Value Detected

**Severity:** high  
**Category:** sensitive_info

## Description

A variable whose name suggests it holds a credential (password, api_key,
secret_key, access_token, private_key, passwd) is assigned a non-empty
string literal in added code. Hardcoded credentials leak through version
control history even after removal.

## Detection

Case-insensitive regex on added lines:

```text
(password|passwd|api_key|secret_key|access_token|private_key)\s*[:=]+\s*["'`][^"'`]{3,}["'`]
```

Evidence is redacted before storage (`[REDACTED]` replaces the value).

## Fix

Use environment variables or a secrets manager:

```go
apiKey := os.Getenv("MY_API_KEY")
```
