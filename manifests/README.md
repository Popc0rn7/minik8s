# Manifests Inventory

`manifests/` only keeps YAML and source assets used by the final acceptance
scripts and the selected serverless demo. Historical ad-hoc examples were
removed so deploy syncs do not copy unused demo material to `/opt/minik8s`.

## Acceptance Scripts

| Path | Used by |
| --- | --- |
| `cni/mooring.yaml` | `scripts/acceptance/01_node_multinode.sh` |
| `pod/pod_02_acceptance_*.yaml` | `scripts/acceptance/02_pod_lifecycle.sh` |
| `service/*_03_*.yaml` | `scripts/acceptance/03_service.sh` |
| `replicaset/*_04_*.yaml` | `scripts/acceptance/04_replicaset.sh` |
| `hpa/*_05_*.yaml` | `scripts/acceptance/05_hpa.sh` |
| `dns/*_06_*.yaml` | `scripts/acceptance/06_dns_forwarding.sh` |
| `fault/*_07_*.yaml` | `scripts/acceptance/07_fault_tolerance.sh` |
| `job/*` | `scripts/acceptance/20_personal_gpu.sh` |

## Serverless Demo

`serverless/harbor-incident-triage/` contains the final selected Serverless
demo manifests, inputs, and wrk script used by `README_ACCEPTANCE.md`.

Do not re-add broad sample manifests here unless they are part of the final
acceptance route. Put exploratory material under `demo/` or document it as
non-acceptance material.
