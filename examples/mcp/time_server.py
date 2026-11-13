import sys, json, datetime

def reply(req_id, result):
    sys.stdout.write(json.dumps({"jsonrpc": "2.0", "id": req_id, "result": result}) + "\n")
    sys.stdout.flush()

for line in sys.stdin:
    line = line.strip()
    if not line:
        continue
    try:
        req = json.loads(line)
    except Exception:
        continue
    if "id" not in req:
        continue
    method = req.get("method")
    if method == "initialize":
        reply(req["id"], {"protocolVersion": "2024-11-05", "capabilities": {"tools": {}}, "serverInfo": {"name": "demo", "version": "0.1"}})
    elif method == "tools/list":
        reply(req["id"], {"tools": [{
            "name": "get_time",
            "description": "返回服务器当前时间（ISO8601）",
            "inputSchema": {"type": "object", "properties": {}}
        }]})
    elif method == "tools/call":
        reply(req["id"], {"content": [{"type": "text", "text": datetime.datetime.now().isoformat(timespec="seconds")}]})
    else:
        reply(req["id"], {})
