# Logbook And Recovery Testcase

Final acceptance covers control-plane restart and object recovery through the
fault-tolerance script.

```bash
source scripts/acceptance/env.sh
bash scripts/acceptance/07_fault_tolerance.sh
```

Cleanup:

```bash
source scripts/acceptance/env.sh
bash scripts/acceptance/07_fault_tolerance.sh cleanup
```
