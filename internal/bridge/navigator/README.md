# Navigator

This module is reserved for bridge scheduling logic.

Current Pod manifests use `spec.nodeName` directly, so sailer selects work via
the Harbor's node-scoped Pod list. A future navigator should watch
unscheduled Pods, choose a node, and write `spec.nodeName` through the control
plane API.
