import json
import sys


def compact(data):
    return json.dumps(data, separators=(",", ":"), sort_keys=True)


def append_trace(event, step):
    trace = list(event.get("trace", []))
    trace.append(step)
    event["trace"] = trace
    event["executedSteps"] = trace
    return event


def diagnosis_for(category):
    if category == "network":
        return [
            "Critical network incident: verify NodePort/firewall/security group reachability first.",
            "Then check Service selector, endpoints, proxy rules, and CNI/VXLAN health.",
        ]
    if category == "runtime":
        return [
            "Critical runtime incident: inspect crash loop, memory limit, exit code, and previous container logs.",
            "Check whether a bad rollout or missing mounted file caused the startup failure.",
        ]
    if category == "build":
        return [
            "Critical build incident: verify registry, proxy, package mirror, and base image availability.",
            "Rebuild with verbose logs and the same environment variables used by CI.",
        ]
    if category == "app":
        return [
            "Critical application incident: inspect upstream health, route mapping, and recent error logs.",
            "Check whether all requests fail or only one route/backend is affected.",
        ]
    return ["Critical incident detected but no strong category matched. Gather cluster events and service endpoints."]


def main(event):
    category = str(event.get("category", "unknown"))
    out = dict(event)
    out.update(
        {
            "ok": True,
            "workflow": "harbor-incident-triage",
            "step": "notify_captain",
            "notified": True,
            "notification": "Captain notified: critical incident detected.",
            "diagnosisType": category,
            "diagnosis": diagnosis_for(category),
        }
    )
    return append_trace(out, "notify_captain")


def handler(event):
    payload = json.loads(event or "{}")
    return compact(main(payload))


if __name__ == "__main__":
    print(json.dumps(main(json.load(sys.stdin)), indent=2, sort_keys=True))
