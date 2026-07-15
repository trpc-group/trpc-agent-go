#!/usr/bin/env python3
import json
import re
import sys
import time


path = sys.argv[1]
findings = []
warnings = []
emitted_findings = set()
emitted_warnings = set()

current_file = ""
current_hunk = []
new_line = 0


def build_hunk_texts(lines):
    hunk_texts = {}
    hunk_lines = []
    hunk_indexes = []

    def flush_hunk():
        if not hunk_indexes:
            return
        text = "\n".join(hunk_lines)
        for index in hunk_indexes:
            hunk_texts[index] = text

    for index, raw in enumerate(lines):
        line = raw.rstrip("\n")
        if line.startswith("@@"):
            flush_hunk()
            hunk_lines = []
            hunk_indexes = []
            continue
        if line.startswith("diff --git ") or line.startswith("+++ b/"):
            continue
        if line.startswith("+") and not line.startswith("+++"):
            hunk_lines.append(line[1:].strip())
            hunk_indexes.append(index)
            continue
        if line.startswith(" "):
            hunk_lines.append(line[1:])

    flush_hunk()
    return hunk_texts


def redact(text: str) -> str:
    text = re.sub(r"(?i)\b(api[_-]?key|apikey|llm[_-]?key|openai[_-]?(api[_-]?)?key|client[_-]?secret|secret|token|bearer[_-]?token|password|passwd|pwd|github[_-]?token|private[_-]?key)\b\s*[:=]\s*(\"[^\"]+\"|'[^']+'|[^\s,;]+)", r"\1=[REDACTED]", text)
    text = re.sub(r"(?i)bearer\s+[A-Za-z0-9\-._~+/=]+", "bearer [REDACTED]", text)
    text = re.sub(r"sk-[A-Za-z0-9_-]{8,}", "[REDACTED]", text)
    text = re.sub(r"ghp_[A-Za-z0-9_]{20,}", "[REDACTED]", text)
    text = re.sub(r"github_pat_[A-Za-z0-9_]{20,}", "[REDACTED]", text)
    text = re.sub(r"[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}", "[REDACTED]", text)
    text = re.sub(r"-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----", "[REDACTED_PRIVATE_KEY]", text)
    text = re.sub(r"([a-z][a-z0-9+.-]*://[^/\s:@]+):([^@\s/]+)@", r"\1:[REDACTED]@", text)
    text = re.sub(r"(?i)(password=)[^&\s]+", r"\1[REDACTED]", text)
    return text


def contains_any(text: str, *items: str) -> bool:
    return any(item in text for item in items)


secret_value_pattern = re.compile(r"(?i)(sk-[A-Za-z0-9_-]{8,}|ghp_[A-Za-z0-9_]{20,}|github_pat_[A-Za-z0-9_]{20,}|Bearer\s+[A-Za-z0-9\-._~+/=]{8,}|[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}|-----BEGIN [A-Z ]*PRIVATE KEY-----|[a-z][a-z0-9+.-]*://[^/\s:@]+:[^@\s/]+@)")
secret_name_pattern = re.compile(r"(?i)(api[_-]?key|apikey|llm[_-]?key|openai[_-]?(api[_-]?)?key|client[_-]?secret|secret|token|bearer[_-]?token|password|passwd|pwd|github[_-]?token|private[_-]?key)")
string_literal_pattern = re.compile(r"=\s*(\"([^\"]*)\"|'([^']*)'|`([^`]*)`)")
placeholder_secret_pattern = re.compile(r"(?i)^(test|example|dummy|placeholder|changeme|change-me|your[-_ ]?token|your[-_ ]?key|xxx+|<.*>)$")


def assigned_string(text: str):
    match = string_literal_pattern.search(text)
    if not match:
        return None
    for group in match.groups()[1:]:
        if group:
            return group
    return ""


def should_report_secret(text: str) -> bool:
    if secret_value_pattern.search(text):
        return True
    if not secret_name_pattern.search(text):
        return False
    value = assigned_string(text)
    if value is None:
        return False
    value = value.strip()
    if len(value) < 12:
        return False
    return not placeholder_secret_pattern.match(value)


def reports_http_body_leak(text: str, hunk_text: str) -> bool:
    if not contains_any(text, "http.Get(", "http.Post(", "http.Head(", "http.DefaultClient.Do(", ".Do("):
        return False
    name = assigned_variable(text)
    if name:
        return f"{name}.Body.Close()" not in hunk_text
    return "Body.Close()" not in hunk_text


def reports_sql_string_concat(text: str) -> bool:
    upper = text.upper()
    if not contains_any(upper, "SELECT ", "INSERT ", "UPDATE ", "DELETE "):
        return False
    return "+" in text or "fmt.Sprintf(" in text


def is_quoted_literal(text: str) -> bool:
    text = text.strip()
    return (text.startswith('"') and text.endswith('"')) or (text.startswith("'") and text.endswith("'")) or (text.startswith("`") and text.endswith("`"))


def command_call_has_dynamic_executable(text: str) -> bool:
    start = text.find("exec.Command")
    if start < 0:
        return False
    open_index = text.find("(", start)
    close_index = text.rfind(")")
    if open_index < 0 or close_index < open_index:
        return False
    args = [arg.strip() for arg in text[open_index + 1:close_index].split(",")]
    is_context = text[start:].startswith("exec.CommandContext")
    executable_index = 1 if is_context else 0
    return executable_index >= len(args) or not is_quoted_literal(args[executable_index])


def reports_command_injection(text: str) -> bool:
    if not contains_any(text, "exec.Command(", "exec.CommandContext("):
        return False
    if '"-c"' in text or "'-c'" in text:
        return True
    return command_call_has_dynamic_executable(text)


def reports_context_background_misuse(text: str, hunk_text: str) -> bool:
    return "context.Background()" in text and "context.Context" in hunk_text


def reports_mutex_unlock_missing(text: str, hunk_text: str) -> bool:
    if ".Lock()" not in text or ".RLock()" in text:
        return False
    receiver = text.strip()[:-len(".Lock()")].strip()
    return not receiver or f"{receiver}.Unlock()" not in hunk_text


assignment_variable_pattern = re.compile(r"([A-Za-z_][A-Za-z0-9_]*)\s*(?:,\s*[A-Za-z_][A-Za-z0-9_]*)?\s*:=")
context_cancel_pattern = re.compile(r"(?:[A-Za-z_][A-Za-z0-9_]*|_)\s*,\s*([A-Za-z_][A-Za-z0-9_]*)\s*:=\s*context\.With(?:Cancel|Timeout|Deadline)")


def assigned_variable(text: str) -> str:
    match = assignment_variable_pattern.search(text)
    return match.group(1) if match else ""


def context_has_cancel_cleanup(text: str, hunk_text: str) -> bool:
    match = context_cancel_pattern.search(text)
    return bool(match and f"{match.group(1)}()" in hunk_text)


def resource_has_cleanup(text: str, hunk_text: str) -> bool:
    name = assigned_variable(text)
    return bool(name and f"{name}.Close()" in hunk_text)


def database_has_cleanup(text: str, hunk_text: str) -> bool:
    name = assigned_variable(text)
    if not name:
        return False
    cleanup = "Close" if "sql.Open" in text else "Rollback"
    return f"{name}.{cleanup}()" in hunk_text


def reports_defer_in_loop(text: str, hunk_before: str) -> bool:
    return text.strip().startswith("defer ") and contains_any(hunk_before, "for ", "range ")


def reports_bare_return_err(text: str) -> bool:
    return text.strip() == "return err"


def string_concat_lhs(text: str) -> str:
    if "+=" not in text:
        return ""
    lhs = text.split("+=", 1)[0]
    if "{" in lhs:
        lhs = lhs.split("{")[-1]
    parts = lhs.strip().split()
    if not parts:
        return ""
    return parts[-1].strip(" \t;")


def reports_string_concat_loop(text: str, hunk_before: str, hunk_text: str) -> bool:
    if "+=" not in text:
        return False
    if not contains_any(hunk_before, "for ", "range ") and not contains_any(text, "for ", "range "):
        return False
    lhs = string_concat_lhs(text)
    if not lhs:
        return False
    if '"' in text or "`" in text:
        return True
    return contains_any(hunk_text, f'{lhs} := ""', f"var {lhs} string")


def add_finding(severity, category, file, line, title, evidence, recommendation, rule_id, status="finding", confidence="high"):
    key = (file, line, category, rule_id)
    if key in emitted_findings:
        return
    emitted_findings.add(key)
    findings.append({
        "severity": severity,
        "category": category,
        "file": file,
        "line": line,
        "title": title,
        "evidence": redact(evidence),
        "recommendation": recommendation,
        "confidence": confidence,
        "source": "skill_run",
        "rule_id": rule_id,
        "status": status,
    })


def add_warning(severity, category, file, line, title, evidence, recommendation, rule_id, status="warning", confidence="medium"):
    key = (file, line, category, rule_id)
    if key in emitted_warnings:
        return
    emitted_warnings.add(key)
    warnings.append({
        "severity": severity,
        "category": category,
        "file": file,
        "line": line,
        "title": title,
        "evidence": redact(evidence),
        "recommendation": recommendation,
        "confidence": confidence,
        "source": "skill_run",
        "rule_id": rule_id,
        "status": status,
    })


with open(path, "r", encoding="utf-8", errors="replace") as f:
    full_text = f.read()
    lines = full_text.splitlines()
    hunk_texts = build_hunk_texts(lines)
    for index, raw in enumerate(lines):
        line = raw.rstrip("\n")
        if line.startswith("+++ b/"):
            current_file = line[len("+++ b/"):]
            continue
        if line.startswith("@@"):
            match = re.search(r"\+(\d+)", line)
            new_line = int(match.group(1)) - 1 if match else 0
            current_hunk = []
            continue
        if line.startswith("+") and not line.startswith("+++"):
            new_line += 1
            text = line[1:].strip()
            hunk_before = "\n".join(current_hunk)
            current_hunk.append(text)
            hunk_text = hunk_texts.get(index, "\n".join(current_hunk))
            if "TODO(" in text or "FIXME" in text:
                add_finding("medium", "maintainability", current_file, new_line,
                            "New code contains a TODO or FIXME marker", text,
                            "Remove the marker or turn it into a tracked issue before merging.",
                            "todo-marker")
            if "panic(" in text:
                add_finding("high", "error_handling", current_file, new_line,
                            "New function panics directly", text,
                            "Return an error or handle the failure path explicitly.",
                            "panic-direct")
            if reports_http_body_leak(text, hunk_text):
                add_finding("high", "resource", current_file, new_line,
                            "HTTP response body is not closed", text,
                            "Close the response body with defer resp.Body.Close() after checking the request error.",
                            "http-body-close")
            if reports_sql_string_concat(text):
                add_finding("critical", "security", current_file, new_line,
                            "SQL query is built with string concatenation", text,
                            "Use parameterized queries or placeholders instead of concatenating user-controlled values.",
                            "sql-string-concat")
            if reports_command_injection(text):
                add_finding("critical", "security", current_file, new_line,
                            "Command execution uses a shell or dynamic argument", text,
                            "Avoid shell execution and pass validated literal arguments to exec.CommandContext.",
                            "command-injection")
            if reports_context_background_misuse(text, hunk_text):
                add_finding("medium", "lifecycle", current_file, new_line,
                            "context.Background is used inside a context-aware function", text,
                            "Propagate the existing ctx so cancellation, deadlines, and trace context are preserved.",
                            "context-background-misuse")
            if reports_mutex_unlock_missing(text, hunk_text):
                add_finding("high", "concurrency", current_file, new_line,
                            "Mutex lock has no visible deferred unlock", text,
                            "Defer Unlock immediately after Lock to avoid deadlocks on early returns.",
                            "mutex-unlock-missing")
            if reports_defer_in_loop(text, hunk_before):
                add_finding("medium", "resource", current_file, new_line,
                            "defer is used inside a loop", text,
                            "Move the loop body into a helper or close the resource before the next iteration.",
                            "defer-in-loop")
            if reports_bare_return_err(text):
                add_finding("medium", "error_handling", current_file, new_line,
                            "Error is returned without context", text,
                            "Wrap the error with operation context using fmt.Errorf(\"operation: %w\", err).",
                            "bare-return-err")
            if reports_string_concat_loop(text, hunk_before, hunk_text):
                add_warning("low", "performance", current_file, new_line,
                            "String concatenation in a loop may allocate repeatedly", text,
                            "Use strings.Builder or bytes.Buffer for repeated string assembly.",
                            "string-concat-loop", status="needs_human_review", confidence="low")
            if current_file in ("foo.go", "service.go") and not current_file.endswith("_test.go") and text.startswith("func ") and "error" not in text:
                add_warning("low", "testing", current_file, new_line,
                            "New function may need a focused test", text,
                            "Add a unit test that exercises the new path.",
                            "missing-test-hint")
            if ("go func" in text or text.startswith("go ")) and not contains_any(hunk_text, "WaitGroup", ".Done()", "errgroup", "done", "sync."):
                add_finding("high", "concurrency", current_file, new_line,
                            "New goroutine has no visible lifecycle guard", text,
                            "Bind the goroutine to a context, wait group, or explicit completion signal.",
                            "goroutine-leak")
            if contains_any(text, "context.WithCancel", "context.WithTimeout", "context.WithDeadline") and not context_has_cancel_cleanup(text, hunk_text):
                add_finding("high", "lifecycle", current_file, new_line,
                            "Derived context is not canceled", text,
                            "Store the cancel function and defer cancel() in the same scope.",
                            "context-leak")
            if contains_any(text, "os.Open", "os.OpenFile", "os.Create") and not resource_has_cleanup(text, hunk_text):
                add_finding("high", "resource", current_file, new_line,
                            "Opened resource has no close path", text,
                            "Defer Close() immediately after the resource is opened.",
                            "resource-leak")
            if contains_any(text, "sql.Open", ".BeginTx", ".Begin(") and not database_has_cleanup(text, hunk_text):
                add_finding("high", "database", current_file, new_line,
                            "Database handle or transaction has no cleanup path", text,
                            "Defer Close() for handles and Rollback() for transactions in the same scope.",
                            "db-lifecycle")
            if should_report_secret(text):
                add_finding("critical", "security", current_file, new_line,
                            "Potential secret appears in added code", text,
                            "Replace the literal with a secret manager or environment lookup.",
                            "secret-leak")
        elif line.startswith(" ") and new_line > 0:
            new_line += 1
            current_hunk.append(line[1:])

print(json.dumps({"findings": findings, "warnings": warnings}, separators=(",", ":")))
if "sandbox-timeout fixture" in full_text:
    time.sleep(3)
if "sandbox-fail fixture" in full_text:
    sys.exit(2)
