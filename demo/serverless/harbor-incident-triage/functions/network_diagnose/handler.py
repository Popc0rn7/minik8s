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


def finish(event, diagnosis_type, diagnosis):
    out = dict(event)
    out.update(
        {
            "ok": True,
            "workflow": "harbor-incident-triage",
            "step": "network_diagnose",
            "diagnosisType": diagnosis_type,
            "diagnosis": diagnosis,
            "notified": bool(out.get("notified", False)),
        }
    )
    return append_trace(out, "network_diagnose")


def main(event):
    text = str(event.get("normalizedText", ""))
    diagnosis = [
        "Pod is running and Service endpoint exists, so the workload may be healthy.",
        "Check Service selector, Pod labels, and endpoint list first.",
    ]
    if "nodeport" in text:
        diagnosis.append("NodePort timeout from outside often indicates host firewall or cloud security group rules.")
    if "iptables" in text or "ipvs" in text:
        diagnosis.append("Check service proxy rules on the target node.")
    if "cni" in text or "vxlan" in text:
        diagnosis.append("Check cross-node Pod network, VXLAN port reachability, and node routes.")
    if "dns" in text:
        diagnosis.append("Check DNS object paths, service names, and in-cluster resolver configuration.")
    return finish(event, "network", diagnosis)


def handler(event):
    payload = json.loads(event or "{}")
    return compact(main(payload))


if __name__ == "__main__":
    print(json.dumps(main(json.load(sys.stdin)), indent=2, sort_keys=True))
