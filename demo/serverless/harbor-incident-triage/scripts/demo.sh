#!/usr/bin/env bash
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/common.sh"

echo "[1] Apply functions"
"$DEMO_ROOT/scripts/apply-functions.sh"

echo "[2] Apply workflow and event trigger"
"$DEMO_ROOT/scripts/apply-workflow.sh"

echo "[3] Functions before invoke; workflow demo fn-* ReplicaSets should be ready"
"$CLI" get functions || true
"$CLI" get replicasets || true

echo "[4] Invoke network incident through Harbor HTTP invoke"
"$DEMO_ROOT/scripts/invoke-network.sh"

echo "[5] Workflow status after branch execution"
"$CLI" describe workflow harbor-incident-triage || true
"$CLI" get workflowruns || true

echo "[6] Invoke build/runtime/app incidents to show other branches"
"$DEMO_ROOT/scripts/invoke-build.sh"
"$DEMO_ROOT/scripts/invoke-runtime.sh"
"$DEMO_ROOT/scripts/invoke-app.sh"

echo "[7] Invoke critical incident to show notify branch"
"$DEMO_ROOT/scripts/invoke-critical.sh"

echo "[8] Request an event to show EventTrigger -> Workflow"
"$MINIK8S" request minik8s.incident.created --data "$(cat "$DEMO_ROOT/inputs/low-risk-incident.json")" --timeout 30s || true
"$CLI" get workflowruns || true

echo "[9] Function pods after cold start"
"$CLI" get pods || true
"$CLI" get replicasets || true

echo "[10] Wait and show steady replicas"
sleep "${IDLE_WAIT_SECONDS:-40}"
"$CLI" get functions || true
"$CLI" get replicasets || true
