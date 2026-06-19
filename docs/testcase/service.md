# Service Testcase

Final acceptance uses fixed manifests under `manifests/service/`.

Run from node-a after multi-node startup:

```bash
source scripts/acceptance/env.sh
bash scripts/acceptance/03_service.sh
```

Cleanup:

```bash
source scripts/acceptance/env.sh
bash scripts/acceptance/03_service.sh cleanup
```
