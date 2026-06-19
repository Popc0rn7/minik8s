# Pod Testcase

Final acceptance now uses fixed manifests under `manifests/pod/`.

Run from node-a after `scripts/acceptance/01_node_multinode.sh` has started the
cluster:

```bash
source scripts/acceptance/env.sh
bash scripts/acceptance/02_pod_lifecycle.sh
```

Cleanup:

```bash
source scripts/acceptance/env.sh
bash scripts/acceptance/02_pod_lifecycle.sh cleanup
```
