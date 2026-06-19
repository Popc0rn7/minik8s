# HPA Testcase

Final acceptance uses fixed manifests under `manifests/hpa/`.

Run from node-a after multi-node startup and metrics addon readiness:

```bash
source scripts/acceptance/env.sh
bash scripts/acceptance/05_hpa.sh
```

Cleanup:

```bash
source scripts/acceptance/env.sh
bash scripts/acceptance/05_hpa.sh cleanup
```
