# CNI Testcase

Final acceptance applies `manifests/cni/mooring.yaml` through the multi-node
startup script.

Run from node-a:

```bash
source scripts/acceptance/env.sh
bash scripts/acceptance/01_node_multinode.sh bridge
bash scripts/acceptance/01_node_multinode.sh
```

Pod-level network evidence is covered by:

```bash
source scripts/acceptance/env.sh
bash scripts/acceptance/03_service.sh
```
