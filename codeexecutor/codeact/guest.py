import asyncio
import contextlib
import io
import json
import sys
import uuid

def send(v):
    sys.__stdout__.write(json.dumps(v, separators=(",", ":")) + "\n")
    sys.__stdout__.flush()

async def call_tool(name, **kwargs):
    call_id = str(uuid.uuid4())
    send({"type":"tool_call", "id":call_id, "name":name, "args":kwargs})
    line = sys.stdin.readline()
    if not line:
        raise RuntimeError("host closed tool bridge")
    reply = json.loads(line)
    if reply.get("type") != "tool_result" or reply.get("id") != call_id:
        raise RuntimeError("invalid tool bridge response")
    if reply.get("error"):
        raise RuntimeError(reply["error"])
    return reply.get("result")

async def main(code):
    scope = {"call_tool": call_tool, "asyncio": asyncio}
    wrapped = "async def __codeact_main__():\n" + "\n".join("    " + line for line in code.splitlines())
    exec(compile(wrapped, "<codeact>", "exec"), scope, scope)
    return await scope["__codeact_main__"]()

line = sys.stdin.readline()
try:
    request = json.loads(line)
    stdout = io.StringIO()
    with contextlib.redirect_stdout(stdout):
        result = asyncio.run(main(request["code"]))
    send({"type":"complete", "args":result, "code":stdout.getvalue()})
except BaseException as exc:
    send({"type":"complete", "name":type(exc).__name__ + ": " + str(exc), "args":None, "code":""})
