import collections, json, sys, urllib.request, urllib.error
# Compares two running servers (old = reference, new = candidate) on the same database, read endpoint by read
# endpoint, byte for byte (only key order and a trailing newline are ignored). Prints paths and kinds of
# differences, never response data.   usage: compare.py <old-base-url> <new-base-url> <username>
R, G, UNAME = sys.argv[1], sys.argv[2], sys.argv[3]
def call(base, method, path, token=None, body=None):
    req = urllib.request.Request(base+path, method=method, data=None if body is None else json.dumps(body).encode(),
        headers={"content-type":"application/json", **({"authorization":"Bearer "+token} if token else {})})
    try:
        with urllib.request.urlopen(req) as r: return r.status, r.read()
    except urllib.error.HTTPError as e: return e.code, e.read()
t = json.loads(call(R,"POST","/auth/dev-login",None,{"username":UNAME})[1])["access_token"]
for d,w in [("2026-10-01",86.4),("2026-10-15",85.95),("2026-11-02",85.4)]:
    call(R,"PUT","/body-weights/"+d,t,{"weight_kg":w})
import os
from decimal import Decimal
if os.environ.get("COMPARE_NUMBERS") == "value":   # 90 and 90.0 are the same number
    num = lambda x: ("N", str(Decimal(x).normalize()))
    strict = lambda b: json.loads(b, parse_float=num, parse_int=num) if b else None
else:                                               # the literal text must match too
    strict = lambda b: json.loads(b, parse_float=lambda x:("F",x), parse_int=lambda x:("I",x)) if b else None
paths = ["/me","/tags","/gyms","/gyms?q=cult","/exercises?q=bench","/exercises?q=incline%20db","/exercises?q=squat&limit=5",
         "/workouts","/workouts?limit=5","/stats/consistency","/stats/consistency?weeks=4","/stats/consistency?weeks=104",
         "/stats/exercises","/body-weights"]
for w in json.loads(call(R,"GET","/workouts?limit=100",t)[1]): paths.append(f"/workouts/{w['id']}")
for e in json.loads(call(R,"GET","/stats/exercises",t)[1]):
    eid = e.get("exercise_id") or e.get("id"); vk = e.get("variant_key")
    paths += [f"/stats/exercises/{eid}/progress", f"/exercises/{eid}/last" + (f"?variant_key={vk}" if vk else "")]
for tz in ["America/New_York","Pacific/Kiritimati"]:
    call(R,"PATCH","/me",t,{"timezone":tz}); paths += ["/stats/consistency", "/stats/exercises"]
def leaves(a,b,path,out):
    if isinstance(a,dict) and isinstance(b,dict):
        for k in sorted(set(a)|set(b)):
            if k not in a or k not in b: out[(path+"/"+k,"only on one side")] += 1; continue
            leaves(a[k],b[k],path+"/"+k,out)
    elif isinstance(a,list) and isinstance(b,list):
        if len(a)!=len(b): out[(path,"list length")] += 1; return
        for x,y in zip(a,b): leaves(x,y,path+"[]",out)
    elif a!=b: out[(path, "number format" if isinstance(a,tuple) else "value")] += 1
n = same = reordered = 0; out = collections.Counter(); status = []
for p in paths:
    (rs,rb),(gs,gb) = call(R,"GET",p,t), call(G,"GET",p,t); n += 1
    if rs != gs: status.append((p,rs,gs)); continue
    if rb.rstrip(b"\n") == gb.rstrip(b"\n"): same += 1; continue
    if strict(rb) == strict(gb): reordered += 1; continue   # only the order of keys differs: nothing reads it
    leaves(strict(rb), strict(gb), "", out)
print(f"requests: {n}  byte-identical: {same}  same but key order: {reordered}  differing: {n-same-reordered}  status mismatches: {len(status)}")
for (path,kind),c in out.most_common(12): print(f"  {c:4d}  {kind:14s} {path}")
for s in status[:5]: print("  STATUS", s)
