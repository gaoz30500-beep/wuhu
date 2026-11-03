import subprocess, json, sys

proc = subprocess.Popen(
    ["go", "run", ".", "--mcp"],
    cwd=r"E:\laji\项目\AGENT",
    stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
    text=True, encoding="utf-8", bufsize=1
)

def send(obj):
    proc.stdin.write(json.dumps(obj, ensure_ascii=False) + "\n")
    proc.stdin.flush()

def recv():
    line = proc.stdout.readline()
    return line.strip() if line else "<EOF>"

send({"jsonrpc":"2.0","id":1,"method":"initialize","params":{
    "protocolVersion":"2024-11-05","capabilities":{},
    "clientInfo":{"name":"test","version":"1.0"}}})
print("INIT:", recv())

send({"jsonrpc":"2.0","method":"notifications/initialized"})

send({"jsonrpc":"2.0","id":2,"method":"tools/list"})
print("TOOLS:", recv())

send({"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"get_location","arguments":{}}})
print("CALL:", recv())

proc.kill()
