#!/usr/bin/env python3
import json
import re
import sys


path = sys.argv[1]
findings = []
warnings = []
emitted_findings = set()
emitted_warnings = set()

current_file = ""
current_hunk = []
new_line = 0


def hunk_counts(line: str):
    match = re.match(r"^@@ -\d+(?:,(\d+))? \+\d+(?:,(\d+))? @@", line)
    if not match:
        return None
    old_count = int(match.group(1)) if match.group(1) is not None else 1
    new_count = int(match.group(2)) if match.group(2) is not None else 1
    return old_count, new_count


class GoStructureScanner:
    def __init__(self):
        self.quote = None
        self.block_comment = False
        self.escape = False

    def scan(self, line: str):
        tokens = []
        index = 0
        while index < len(line):
            char = line[index]
            next_char = line[index + 1] if index + 1 < len(line) else ""
            if self.block_comment:
                if char == "*" and next_char == "/":
                    self.block_comment = False
                    index += 2
                else:
                    index += 1
                continue
            if self.quote is not None:
                if self.quote != "`" and self.escape:
                    self.escape = False
                elif self.quote != "`" and char == "\\":
                    self.escape = True
                elif char == self.quote:
                    self.quote = None
                index += 1
                continue
            if char == "/" and next_char == "/":
                break
            if char == "/" and next_char == "*":
                self.block_comment = True
                index += 2
                continue
            if char in ('"', "'", "`"):
                self.quote = char
                self.escape = False
                index += 1
                continue
            if char.isalpha() or char == "_":
                end = index + 1
                while end < len(line) and (line[end].isalnum() or line[end] == "_"):
                    end += 1
                if line[index:end] == "for":
                    tokens.append("for")
                index = end
                continue
            if char in "{}":
                tokens.append(char)
            index += 1
        return tokens


def is_function_start(text: str) -> bool:
    return text.startswith("func ") or text.startswith("func(")


def analyze_scoped_lines(scoped_lines):
    function_parts = {}
    cleanup_parts = {}
    cleanup_lengths = {}
    for item in scoped_lines:
        value = item["text"] + "\n"
        function_parts.setdefault(item["segment"], []).append(value)
        cleanup_key = (item["segment"], item["block_path"])
        item["cleanup_offset"] = cleanup_lengths.get(cleanup_key, 0)
        cleanup_parts.setdefault(cleanup_key, []).append(value)
        cleanup_lengths[cleanup_key] = item["cleanup_offset"] + len(value)
    function_text = {segment: "".join(parts) for segment, parts in function_parts.items()}
    cleanup_text = {key: "".join(parts) for key, parts in cleanup_parts.items()}
    scopes = {}
    for item in scoped_lines:
        cleanup_key = (item["segment"], item["block_path"])
        cleanup = cleanup_text[cleanup_key][item["cleanup_offset"]:]
        scopes[item["index"]] = {
            "function_text": function_text[item["segment"]],
            "cleanup_text": cleanup,
            "in_loop": item["in_loop"],
        }
    return scopes


def build_hunk_scopes(lines):
    hunk_scopes = {}
    hunk_lines = []
    blocks = []
    segment = 0
    next_block_id = 0
    structure_scanner = GoStructureScanner()
    old_remaining = 0
    new_remaining = 0

    def flush_hunk():
        if not hunk_lines:
            return
        hunk_scopes.update(analyze_scoped_lines(hunk_lines))

    def append_line(index, text):
        nonlocal segment, next_block_id
        text = text.strip()
        if is_function_start(text):
            segment += 1
            blocks.clear()
        tokens = structure_scanner.scan(text)
        while tokens and tokens[0] == "}":
            if blocks:
                blocks.pop()
            tokens.pop(0)
        hunk_lines.append({
            "index": index,
            "text": text,
            "segment": segment,
            "block_path": tuple(block["id"] for block in blocks),
            "in_loop": any(block["is_loop"] for block in blocks),
        })
        pending_loop = False
        for structure_token in tokens:
            if structure_token == "for":
                pending_loop = True
            elif structure_token == "{":
                next_block_id += 1
                blocks.append({"id": next_block_id, "is_loop": pending_loop})
                pending_loop = False
            elif structure_token == "}" and blocks:
                blocks.pop()

    for index, raw in enumerate(lines):
        line = raw.rstrip("\n")
        if line.startswith("diff --git "):
            saw_diff_structure = True
        counts = hunk_counts(line)
        if line.startswith("@@ ") and counts is None:
            raise ValueError(f"invalid hunk header: {line}")
        if counts is not None:
            flush_hunk()
            hunk_lines = []
            blocks = []
            segment = 0
            structure_scanner = GoStructureScanner()
            old_remaining, new_remaining = counts
            continue
        if old_remaining <= 0 and new_remaining <= 0 and (line.startswith("diff --git ") or line.startswith("+++ ")):
            continue
        if new_remaining > 0 and line.startswith("+"):
            append_line(index, line[1:])
            new_remaining -= 1
            continue
        if old_remaining > 0 and new_remaining > 0 and line.startswith(" "):
            append_line(index, line[1:])
            old_remaining -= 1
            new_remaining -= 1
            continue
        if old_remaining > 0 and line.startswith("-"):
            old_remaining -= 1

    flush_hunk()
    return hunk_scopes


def redact(text: str) -> str:
    text = re.sub(r"(?i)\b(api[_-]?key|apikey|llm[_-]?key|openai[_-]?(api[_-]?)?key|client[_-]?secret|secret|token|bearer[_-]?token|password|passwd|pwd|github[_-]?token|private[_-]?key)\b\s*(?::=|[:=])\s*(\"[^\"]+\"|'[^']+'|[^\s,;]+)", r"\1=[REDACTED]", text)
    text = re.sub(r"(?i)bearer\s+[A-Za-z0-9\-._~+/=]+", "bearer [REDACTED]", text)
    text = re.sub(r"sk-[A-Za-z0-9_-]{8,}", "[REDACTED]", text)
    text = re.sub(r"ghp_[A-Za-z0-9_]{20,}", "[REDACTED]", text)
    text = re.sub(r"github_pat_[A-Za-z0-9_]{20,}", "[REDACTED]", text)
    text = re.sub(r"[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}", "[REDACTED]", text)
    text = re.sub(r"-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----", "[REDACTED_PRIVATE_KEY]", text)
    text = re.sub(r"(?i)([a-z][a-z0-9+.-]*://[^/\s:@]+):([^@\s/]+)@", r"\1:[REDACTED]@", text)
    text = re.sub(r"(?i)(password=)[^&\s]+", r"\1[REDACTED]", text)
    return text


def contains_any(text: str, *items: str) -> bool:
    return any(item in text for item in items)


def decode_git_path(raw: str) -> str:
    raw = raw.strip()
    if len(raw) >= 2 and raw[0] == '"' and raw[-1] == '"':
        body = raw[1:-1]
        out = bytearray()
        index = 0
        while index < len(body):
            if body[index] != "\\":
                out.extend(body[index].encode("utf-8"))
                index += 1
                continue
            index += 1
            if index >= len(body):
                out.append(ord("\\"))
                break
            if "0" <= body[index] <= "7":
                digits = body[index:index + 3]
                out.append(int(digits, 8))
                index += len(digits)
                continue
            escapes = {"n": 10, "r": 13, "t": 9, "\\": ord("\\"), '"': ord('"')}
            out.append(escapes.get(body[index], ord(body[index])))
            index += 1
        raw = out.decode("utf-8", errors="replace")
    elif "\t" in raw:
        raw = raw.split("\t", 1)[0].rstrip()
    if raw.startswith("a/") or raw.startswith("b/"):
        raw = raw[2:]
    return raw


def is_go_file(path: str) -> bool:
    return path.endswith(".go")


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
    if contains_any(text, '"-c"', "'-c'", '"-lc"', "'-lc'"):
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


def reports_defer_in_loop(text: str, in_loop: bool) -> bool:
    return text.strip().startswith("defer ") and in_loop


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


def reports_string_concat_loop(text: str, in_loop: bool, function_text: str) -> bool:
    if "+=" not in text:
        return False
    if not in_loop:
        return False
    lhs = string_concat_lhs(text)
    if not lhs:
        return False
    if '"' in text or "`" in text:
        return True
    return contains_any(function_text, f'{lhs} := ""', f"var {lhs} string")


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
    hunk_scopes = build_hunk_scopes(lines)
    old_remaining = 0
    new_remaining = 0
    saw_diff_structure = False
    for index, raw in enumerate(lines):
        line = raw.rstrip("\n")
        counts = hunk_counts(line)
        if old_remaining <= 0 and new_remaining <= 0 and line.startswith("+++ "):
            saw_diff_structure = True
            current_file = decode_git_path(line[len("+++ "):])
            continue
        if counts is not None:
            if old_remaining > 0 or new_remaining > 0:
                raise ValueError("new hunk started before the previous hunk was complete")
            saw_diff_structure = True
            match = re.search(r"\+(\d+)", line)
            new_line = int(match.group(1)) - 1 if match else 0
            old_remaining, new_remaining = counts
            current_hunk = []
            continue
        if new_remaining > 0 and line.startswith("+"):
            new_line += 1
            new_remaining -= 1
            text = line[1:].strip()
            current_hunk.append(text)
            scope = hunk_scopes.get(index, {
                "function_text": "\n".join(current_hunk),
                "cleanup_text": "\n".join(current_hunk),
                "in_loop": False,
            })
            function_text = scope["function_text"]
            cleanup_text = scope["cleanup_text"]
            if "TODO(" in text or "FIXME" in text:
                add_finding("medium", "maintainability", current_file, new_line,
                            "New code contains a TODO or FIXME marker", text,
                            "Remove the marker or turn it into a tracked issue before merging.",
                            "todo-marker")
            if should_report_secret(text):
                add_finding("critical", "security", current_file, new_line,
                            "Potential secret appears in added code", text,
                            "Replace the literal with a secret manager or environment lookup.", "secret-leak")
            if not is_go_file(current_file):
                continue
            if "panic(" in text:
                add_finding("high", "error_handling", current_file, new_line,
                            "New function panics directly", text,
                            "Return an error or handle the failure path explicitly.",
                            "panic-direct")
            if reports_http_body_leak(text, cleanup_text):
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
            if reports_context_background_misuse(text, function_text):
                add_finding("medium", "lifecycle", current_file, new_line,
                            "context.Background is used inside a context-aware function", text,
                            "Propagate the existing ctx so cancellation, deadlines, and trace context are preserved.",
                            "context-background-misuse")
            if reports_mutex_unlock_missing(text, cleanup_text):
                add_finding("high", "concurrency", current_file, new_line,
                            "Mutex lock has no visible deferred unlock", text,
                            "Defer Unlock immediately after Lock to avoid deadlocks on early returns.",
                            "mutex-unlock-missing")
            if reports_defer_in_loop(text, scope["in_loop"]):
                add_finding("medium", "resource", current_file, new_line,
                            "defer is used inside a loop", text,
                            "Move the loop body into a helper or close the resource before the next iteration.",
                            "defer-in-loop")
            if reports_bare_return_err(text):
                add_finding("medium", "error_handling", current_file, new_line,
                            "Error is returned without context", text,
                            "Wrap the error with operation context using fmt.Errorf(\"operation: %w\", err).",
                            "bare-return-err")
            if reports_string_concat_loop(text, scope["in_loop"], function_text):
                add_warning("low", "performance", current_file, new_line,
                            "String concatenation in a loop may allocate repeatedly", text,
                            "Use strings.Builder or bytes.Buffer for repeated string assembly.",
                            "string-concat-loop", status="needs_human_review", confidence="low")
            if current_file.endswith(".go") and not current_file.endswith("_test.go") and text.startswith("func ") and "error" not in text:
                add_warning("low", "testing", current_file, new_line,
                            "New function may need a focused test", text,
                            "Add a unit test that exercises the new path.",
                            "missing-test-hint")
            if ("go func" in text or text.startswith("go ")) and not contains_any(function_text, "WaitGroup", ".Done()", "errgroup", "done", "sync."):
                add_finding("high", "concurrency", current_file, new_line,
                            "New goroutine has no visible lifecycle guard", text,
                            "Bind the goroutine to a context, wait group, or explicit completion signal.",
                            "goroutine-leak")
            if contains_any(text, "context.WithCancel", "context.WithTimeout", "context.WithDeadline") and not context_has_cancel_cleanup(text, cleanup_text):
                add_finding("high", "lifecycle", current_file, new_line,
                            "Derived context is not canceled", text,
                            "Store the cancel function and defer cancel() in the same scope.",
                            "context-leak")
            if contains_any(text, "os.Open", "os.OpenFile", "os.Create") and not resource_has_cleanup(text, cleanup_text):
                add_finding("high", "resource", current_file, new_line,
                            "Opened resource has no close path", text,
                            "Defer Close() immediately after the resource is opened.",
                            "resource-leak")
            if contains_any(text, "sql.Open", ".BeginTx", ".Begin(") and not database_has_cleanup(text, cleanup_text):
                add_finding("high", "database", current_file, new_line,
                            "Database handle or transaction has no cleanup path", text,
                            "Defer Close() for handles and Rollback() for transactions in the same scope.",
                            "db-lifecycle")
        elif old_remaining > 0 and line.startswith("-"):
            old_remaining -= 1
        elif old_remaining > 0 and new_remaining > 0 and line.startswith(" "):
            new_line += 1
            current_hunk.append(line[1:])
            old_remaining -= 1
            new_remaining -= 1
        elif old_remaining > 0 or new_remaining > 0:
            if line != r"\ No newline at end of file":
                raise ValueError(f"invalid hunk line: {line}")
        elif (line.startswith("+") and not line.startswith("+++ ")) or \
                (line.startswith("-") and not line.startswith("--- ")) or line.startswith(" "):
            raise ValueError(f"excess hunk line: {line}")

    if old_remaining != 0 or new_remaining != 0:
        raise ValueError(f"incomplete hunk: {old_remaining} old and {new_remaining} new lines remain")
    if full_text.strip() and not saw_diff_structure:
        raise ValueError("input is not a unified diff")

print(json.dumps({"findings": findings, "warnings": warnings}, separators=(",", ":")))
