# ReplicaSet Testcase

Final acceptance uses fixed manifests under `manifests/replicaset/`.

Run from node-a after multi-node startup:

```bash
source scripts/acceptance/env.sh
bash scripts/acceptance/04_replicaset.sh
```

Cleanup:

```bash
source scripts/acceptance/env.sh
bash scripts/acceptance/04_replicaset.sh cleanup
```
