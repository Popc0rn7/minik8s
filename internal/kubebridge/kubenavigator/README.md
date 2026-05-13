# Kubenavigator

This module is reserved for kubebridge scheduling logic.

Current Pod manifests use `spec.nodeName` directly, so kubesailer selects work via
the Kubeharbor's node-scoped Pod list. A future kubenavigator should watch
unscheduled Pods, choose a node, and write `spec.nodeName` through the control
plane API.
