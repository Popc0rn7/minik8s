# Scheduler

This module is reserved for kubecaptain scheduling logic.

Current Pod manifests use `spec.nodeName` directly, so kubelet selects work via
the API Server's node-scoped Pod list. A future scheduler should watch
unscheduled Pods, choose a node, and write `spec.nodeName` through the control
plane API.
